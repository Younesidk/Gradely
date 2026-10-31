using System.Net;
using Microsoft.AspNetCore.Hosting;
using Microsoft.Extensions.Logging;
using System.Text.RegularExpressions;
using Gradly.Application;
using Gradly.Domain.Entities;
using Gradly.Infrastructure.Data;
using Gradly.Infrastructure.Email;
using Gradly.Infrastructure.Identity;
using Microsoft.AspNetCore.Mvc.Testing;
using Microsoft.AspNetCore.Identity;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.DependencyInjection.Extensions;

namespace Gradly.Tests.Integration;

public sealed class FakeEmail : IEmailService
{
    public bool IsConfigured { get; set; }
    public bool Fail { get; set; }
    public List<EmailMessage> Sent { get; } = [];

    public Task<EmailSendResult> SendAsync(EmailMessage message, CancellationToken ct = default)
    {
        if (!IsConfigured) return Task.FromResult(EmailSendResult.Fail("not configured"));
        if (Fail) return Task.FromResult(EmailSendResult.Fail("provider down"));
        Sent.Add(message);
        return Task.FromResult(EmailSendResult.Ok());
    }
}

/// <summary>Stand-in for the signed-in user so services can be tested without a browser.</summary>
public sealed class FakeCurrentUser : ICurrentUser
{
    public string? UserId { get; set; }
    public bool IsAdmin { get; set; }

    public Task<string> GetUserIdAsync() =>
        UserId is null ? throw new UnauthorizedAccessException() : Task.FromResult(UserId);

    public Task<bool> IsAdminAsync() => Task.FromResult(UserId is not null && IsAdmin);
}

/// <summary>A real Gradly host with its own SQLite file in a temp folder.</summary>
public class GradlyFactory : WebApplicationFactory<Program>
{
    public const string AdminPassword = "Admin-Pass-12345";
    public const string UserPassword = "User-Pass-12345";

    private readonly bool _ownsDirectory;

    public GradlyFactory(string? dataDir = null, string adminPassword = AdminPassword, int rateLimit = 1000,
        string? defaultLabFormula = null, bool bootstrapAdmin = true)
    {
        _ownsDirectory = dataDir is null;
        DataDir = dataDir ?? Path.Combine(Path.GetTempPath(), "gradly-tests-" + Guid.NewGuid().ToString("N"));
        Directory.CreateDirectory(DataDir);
        AdminPasswordUsed = adminPassword;
        RateLimit = rateLimit;
        DefaultLabFormula = defaultLabFormula;
        BootstrapAdmin = bootstrapAdmin;
    }

    public string DataDir { get; }
    public string AdminPasswordUsed { get; }
    public int RateLimit { get; }
    public string? DefaultLabFormula { get; }
    public bool BootstrapAdmin { get; }
    public FakeEmail Email { get; } = new();

    protected override void ConfigureWebHost(IWebHostBuilder builder)
    {
        builder.UseEnvironment("Development");
        builder.UseSetting("GRADLY_DATA_DIR", DataDir);
        builder.UseSetting("GRADLY_AUTH_RATE_LIMIT", RateLimit.ToString());
        builder.UseSetting("APP_BASE_URL", "http://gradly.test");
        if (BootstrapAdmin)
        {
            builder.UseSetting("ADMIN_EMAIL", "admin@gradly.test");
            builder.UseSetting("ADMIN_USERNAME", "admin");
            builder.UseSetting("ADMIN_PASSWORD", AdminPasswordUsed);
        }
        if (DefaultLabFormula is not null) builder.UseSetting("DEFAULT_LAB_FORMULA", DefaultLabFormula);
        builder.ConfigureServices(services =>
        {
            services.RemoveAll<IEmailService>();
            services.AddSingleton<IEmailService>(Email);
        });
        builder.ConfigureLogging(l => l.ClearProviders());
    }

    public HttpClient NewClient() => CreateClient(new WebApplicationFactoryClientOptions
    {
        AllowAutoRedirect = false,
        HandleCookies = true,
    });

    public IDbContextFactory<AppDbContext> Db => Services.GetRequiredService<IDbContextFactory<AppDbContext>>();

    /// <summary>Creates a normal (non-admin) account directly, bypassing invitations.</summary>
    public async Task<ApplicationUser> CreateUserAsync(string username, string? email = null, bool admin = false)
    {
        await using var scope = Services.CreateAsyncScope();
        var provisioner = scope.ServiceProvider.GetRequiredService<UserProvisioner>();
        var result = await provisioner.CreateAsync(email ?? $"{username}@gradly.test", username, UserPassword, admin);
        Assert.True(result.Succeeded, string.Join(";", result.Errors.Select(e => e.Description)));
        var users = scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
        return (await users.FindByNameAsync(username))!;
    }

    public async Task<ApplicationUser> FindUserAsync(string username)
    {
        await using var scope = Services.CreateAsyncScope();
        return (await scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>().FindByNameAsync(username))!;
    }

    public async Task<string> AdminIdAsync() => (await FindUserAsync("admin")).Id;

    // ---- service builders bound to a fake identity ----
    public AcademicService Academic(FakeCurrentUser who) => new(Db, who, TimeProvider.System);
    public FormulaService Formulas(FakeCurrentUser who) => new(Db, who, TimeProvider.System);
    public StorageEstimator NewStorageEstimator() => new(Db, Services.GetRequiredService<GradlyOptions>(), TimeProvider.System);

    public AdminService AdminSvc(FakeCurrentUser who) =>
        new(Db, Services.GetRequiredService<IServiceScopeFactory>(), who,
            Services.GetRequiredService<StorageEstimator>(), TimeProvider.System);

    public InvitationService Invitations(FakeCurrentUser who, TimeProvider? clock = null) =>
        new(Db, who, Email, Services.GetRequiredService<GradlyOptions>(), clock ?? TimeProvider.System);

    public async Task<RegistrationResult> RegisterAsync(string? token, string username, string password = UserPassword, TimeProvider? clock = null)
    {
        await using var scope = Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<AppDbContext>();
        var provisioner = scope.ServiceProvider.GetRequiredService<UserProvisioner>();
        return await new RegistrationService(db, provisioner, clock ?? TimeProvider.System).RegisterAsync(token, username, password);
    }

    public FakeCurrentUser AsAdmin() => new() { UserId = AdminIdSync(), IsAdmin = true };
    public static FakeCurrentUser As(ApplicationUser user) => new() { UserId = user.Id, IsAdmin = false };

    private string AdminIdSync() => AdminIdAsync().GetAwaiter().GetResult();

    protected override void Dispose(bool disposing)
    {
        base.Dispose(disposing);
        if (!disposing || !_ownsDirectory) return;
        Microsoft.Data.Sqlite.SqliteConnection.ClearAllPools();
        try { Directory.Delete(DataDir, recursive: true); } catch (IOException) { /* best effort */ }
    }
}

/// <summary>xUnit class-fixture flavour of <see cref="GradlyFactory"/> (fixtures need one public constructor).</summary>
public sealed class SharedFactory() : GradlyFactory(dataDir: null);

/// <summary>Drives the static SSR forms (login etc.) the way a browser would.</summary>
public static partial class HttpExtensions
{
    [GeneratedRegex(@"name=""__RequestVerificationToken""[^>]*?value=""([^""]+)""")]
    private static partial Regex TokenRegex();

    [GeneratedRegex(@"name=""_handler""[^>]*?value=""([^""]+)""")]
    private static partial Regex HandlerRegex();

    public static async Task<HttpResponseMessage> PostFormAsync(this HttpClient client, string url, IDictionary<string, string> fields, string? pageUrl = null)
    {
        var page = await client.GetAsync(pageUrl ?? url);
        var html = await page.Content.ReadAsStringAsync();
        var data = new Dictionary<string, string>(fields);
        var tok = TokenRegex().Match(html);
        if (tok.Success) data["__RequestVerificationToken"] = tok.Groups[1].Value;
        var handler = HandlerRegex().Match(html);
        if (handler.Success) data["_handler"] = handler.Groups[1].Value;
        return await client.PostAsync(url, new FormUrlEncodedContent(data));
    }

    public static Task<HttpResponseMessage> SignInAsync(this HttpClient client, string identifier, string password) =>
        client.PostFormAsync("/account/login", new Dictionary<string, string>
        {
            ["Input.Identifier"] = identifier,
            ["Input.Password"] = password,
        });

    public static bool IsRedirectTo(this HttpResponseMessage r, string pathPrefix) =>
        (int)r.StatusCode is 301 or 302 or 303 or 307 or 308
        && r.Headers.Location is { } loc
        && (loc.IsAbsoluteUri ? loc.PathAndQuery : loc.OriginalString).StartsWith(pathPrefix, StringComparison.OrdinalIgnoreCase);
}
