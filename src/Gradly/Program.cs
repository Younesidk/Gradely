using System.Threading.RateLimiting;
using Gradly.Application;
using Gradly.Components;
using Gradly.Components.Shared;
using Gradly.Domain.Entities;
using Gradly.Infrastructure.Data;
using Gradly.Infrastructure.Email;
using Gradly.Infrastructure.Identity;
using Microsoft.AspNetCore.Antiforgery;
using Microsoft.AspNetCore.DataProtection;
using Microsoft.AspNetCore.DataProtection.KeyManagement;
using Microsoft.AspNetCore.DataProtection.Repositories;
using Microsoft.AspNetCore.Components.Authorization;
using Microsoft.AspNetCore.HttpOverrides;
using Microsoft.AspNetCore.Identity;
using Microsoft.AspNetCore.RateLimiting;
using Microsoft.EntityFrameworkCore;

var builder = WebApplication.CreateBuilder(args);
var services = builder.Services;
// Cookies follow the request scheme by default (works over http://localhost and behind a proxy that forwards
// X-Forwarded-Proto). Set GRADLY_SECURE_COOKIES=true to always mark cookies Secure (recommended in production).
var cookieSecurePolicy = string.Equals(builder.Configuration["GRADLY_SECURE_COOKIES"], "true", StringComparison.OrdinalIgnoreCase)
    ? CookieSecurePolicy.Always
    : CookieSecurePolicy.SameAsRequest;

// ---------- configuration (resolved lazily so test hosts can override it) ----------
services.AddSingleton(sp => GradlyOptions.From(sp.GetRequiredService<IConfiguration>(), builder.Environment.ContentRootPath));
services.AddSingleton(TimeProvider.System);

// ---------- database: SQLite via EF Core ----------
static void ConfigureSqlite(IServiceProvider sp, DbContextOptionsBuilder o)
{
    var options = sp.GetRequiredService<GradlyOptions>();
    Directory.CreateDirectory(options.DataDirectory);
    o.UseSqlite($"Data Source={options.DatabasePath};Foreign Keys=True;Default Timeout=30")
        .AddInterceptors(new SqlitePragmaInterceptor());
}
// Identity's stores need a scoped AppDbContext; Blazor components use the factory (short-lived contexts).
services.AddDbContext<AppDbContext>(ConfigureSqlite, optionsLifetime: ServiceLifetime.Singleton);
services.AddDbContextFactory<AppDbContext>(ConfigureSqlite);

// ---------- data protection: keep cookie/antiforgery keys next to the database so sessions survive restarts ----------
services.AddDataProtection().SetApplicationName("Gradly");
services.AddOptions<KeyManagementOptions>().Configure<GradlyOptions, ILoggerFactory>((keys, gradly, loggers) =>
{
    var dir = Path.Combine(gradly.DataDirectory, "keys");
    Directory.CreateDirectory(dir);
    keys.XmlRepository = new FileSystemXmlRepository(new DirectoryInfo(dir), loggers);
});

// ---------- identity & authentication ----------
services.AddAuthentication(o =>
{
    o.DefaultScheme = IdentityConstants.ApplicationScheme;
    o.DefaultSignInScheme = IdentityConstants.ExternalScheme;
}).AddIdentityCookies();

services.AddIdentityCore<ApplicationUser>(o =>
    {
        o.User.RequireUniqueEmail = true;
        o.Password.RequiredLength = 10;
        o.Password.RequireDigit = true;
        o.Password.RequireLowercase = true;
        o.Password.RequireUppercase = false;
        o.Password.RequireNonAlphanumeric = false;
        o.SignIn.RequireConfirmedEmail = false;
        o.Lockout.MaxFailedAccessAttempts = 5;
        o.Lockout.DefaultLockoutTimeSpan = TimeSpan.FromMinutes(10);
        o.Lockout.AllowedForNewUsers = true;
        o.User.AllowedUserNameCharacters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._@+";
    })
    .AddRoles<IdentityRole>()
    .AddEntityFrameworkStores<AppDbContext>()
    .AddSignInManager<AppSignInManager>()
    .AddDefaultTokenProviders();

services.ConfigureApplicationCookie(o =>
{
    o.Cookie.Name = ".Gradly.Auth";
    o.Cookie.HttpOnly = true;
    o.Cookie.SameSite = SameSiteMode.Lax;
    o.Cookie.SecurePolicy = cookieSecurePolicy;
    o.LoginPath = "/account/login";
    o.AccessDeniedPath = "/account/access-denied";
    o.ExpireTimeSpan = TimeSpan.FromDays(14);
    o.SlidingExpiration = true;
});
// Validate the security stamp on every request so disabling an account takes effect immediately.
services.Configure<SecurityStampValidatorOptions>(o => o.ValidationInterval = TimeSpan.Zero);
services.AddAntiforgery(o =>
{
    o.Cookie.Name = ".Gradly.Antiforgery";
    o.Cookie.SecurePolicy = cookieSecurePolicy;
});

services.AddAuthorizationBuilder()
    .AddPolicy(Policies.AdminOnly, p => p.RequireAuthenticatedUser().RequireRole(Roles.Admin));

services.AddHttpContextAccessor();
services.AddCascadingAuthenticationState();
services.AddScoped<AuthenticationStateProvider, RevalidatingAuthStateProvider>();
services.AddScoped<ICurrentUser, AuthStateCurrentUser>();

// ---------- rate limiting for authentication-sensitive endpoints ----------
services.AddOptions<RateLimiterOptions>().Configure<GradlyOptions>((limiter, gradly) =>
{
    limiter.RejectionStatusCode = StatusCodes.Status429TooManyRequests;
    limiter.OnRejected = async (ctx, ct) =>
    {
        ctx.HttpContext.Response.Headers.RetryAfter = "60";
        ctx.HttpContext.Response.ContentType = "text/plain";
        await ctx.HttpContext.Response.WriteAsync("Too many attempts. Please wait a minute and try again.", ct);
    };
    limiter.GlobalLimiter = PartitionedRateLimiter.Create<HttpContext, string>(http =>
    {
        var path = http.Request.Path;
        var sensitive = path.StartsWithSegments("/account") &&
                        (HttpMethods.IsPost(http.Request.Method)
                         || path.StartsWithSegments("/account/register")
                         || path.StartsWithSegments("/account/confirm-email-change"));
        if (!sensitive) return RateLimitPartition.GetNoLimiter("none");
        return RateLimitPartition.GetFixedWindowLimiter(
            http.Connection.RemoteIpAddress?.ToString() ?? "unknown",
            _ => new FixedWindowRateLimiterOptions
            {
                PermitLimit = gradly.AuthRequestsPerMinute,
                Window = TimeSpan.FromMinutes(1),
                QueueLimit = 0,
            });
    });
});
services.AddRateLimiter(_ => { });

// ---------- application services ----------
services.AddScoped<UserProvisioner>();
services.AddScoped<AcademicService>();
services.AddScoped<FormulaService>();
services.AddScoped<InvitationService>();
services.AddScoped<RegistrationService>();
services.AddScoped<AdminService>();
services.AddScoped<AccountService>();
services.AddSingleton<StorageEstimator>();
services.AddScoped<ToastService>();
services.AddScoped<ConfirmService>();
services.AddHttpClient<IEmailService, ResendEmailService>(c =>
{
    c.BaseAddress = new Uri("https://api.resend.com/");
    c.Timeout = TimeSpan.FromSeconds(15);
});

services.AddRazorComponents().AddInteractiveServerComponents();

// Behind a TLS-terminating proxy (Cloudflare tunnel, Dokploy) the app only sees http; trust X-Forwarded-* so
// IsHttps, secure cookies and antiforgery work. The container is only reachable through that proxy.
services.Configure<ForwardedHeadersOptions>(o =>
{
    o.ForwardedHeaders = ForwardedHeaders.XForwardedFor | ForwardedHeaders.XForwardedProto | ForwardedHeaders.XForwardedHost;
    o.KnownIPNetworks.Clear();
    o.KnownProxies.Clear();
});

var app = builder.Build();

app.UseForwardedHeaders();

await DatabaseInitializer.MigrateAsync(app.Services);
await AdminBootstrapper.RunAsync(app.Services, app.Configuration, app.Logger);

if (!app.Environment.IsDevelopment())
{
    app.UseExceptionHandler("/Error", createScopeForErrors: true);
}

app.Use(async (ctx, next) =>
{
    var h = ctx.Response.Headers;
    h["X-Content-Type-Options"] = "nosniff";
    h["X-Frame-Options"] = "DENY";
    h["Referrer-Policy"] = "same-origin";
    h["Permissions-Policy"] = "camera=(), microphone=(), geolocation=()";
    await next();
});
app.UseStatusCodePagesWithReExecute("/not-found", createScopeForStatusCodePages: true);
app.UseRateLimiter();
app.UseAuthentication();
app.UseAuthorization();
app.UseAntiforgery();

app.MapStaticAssets();
app.MapRazorComponents<App>().AddInteractiveServerRenderMode();

var account = app.MapGroup("/account");

// Sign-out is a POST protected by an antiforgery token (a plain link could be triggered cross-site).
account.MapPost("/logout", async (HttpContext http, IAntiforgery antiforgery, SignInManager<ApplicationUser> signIn) =>
{
    try { await antiforgery.ValidateRequestAsync(http); }
    catch (AntiforgeryValidationException) { return Results.BadRequest("Invalid request."); }
    await signIn.SignOutAsync();
    return Results.LocalRedirect("/account/login?notice=signed-out");
});

// Re-issues the auth cookie so renamed users see their new name without signing in again.
account.MapGet("/refresh", async (HttpContext http, SignInManager<ApplicationUser> signIn, UserManager<ApplicationUser> users, string? returnUrl) =>
{
    var user = await users.GetUserAsync(http.User);
    if (user is null || user.IsDisabled) return Results.LocalRedirect("/account/login");
    await signIn.RefreshSignInAsync(user);
    return Results.LocalRedirect(LocalUrl.Safe(returnUrl));
}).RequireAuthorization();

app.Run();

public static class Policies
{
    public const string AdminOnly = "AdminOnly";
}

/// <summary>Only same-site relative paths are acceptable redirect targets (prevents open redirects).</summary>
public static class LocalUrl
{
    public static string Safe(string? url, string fallback = "/")
    {
        if (string.IsNullOrWhiteSpace(url)) return fallback;
        if (url[0] != '/' || url.Length > 1 && (url[1] == '/' || url[1] == '\\')) return fallback;
        if (url.Contains('\r') || url.Contains('\n')) return fallback;
        return url;
    }
}

public partial class Program;
