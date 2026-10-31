using System.Security.Claims;
using Gradly.Application;
using Gradly.Domain.Entities;
using Gradly.Infrastructure.Data;
using Microsoft.AspNetCore.Components.Authorization;
using Microsoft.AspNetCore.Components.Server;
using Microsoft.AspNetCore.Identity;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Options;

namespace Gradly.Infrastructure.Identity;

/// <summary>Refuses to sign in disabled accounts.</summary>
public sealed class AppSignInManager(
    UserManager<ApplicationUser> userManager,
    IHttpContextAccessor contextAccessor,
    IUserClaimsPrincipalFactory<ApplicationUser> claimsFactory,
    IOptions<IdentityOptions> optionsAccessor,
    ILogger<SignInManager<ApplicationUser>> logger,
    Microsoft.AspNetCore.Authentication.IAuthenticationSchemeProvider schemes,
    IUserConfirmation<ApplicationUser> confirmation)
    : SignInManager<ApplicationUser>(userManager, contextAccessor, claimsFactory, optionsAccessor, logger, schemes, confirmation)
{
    public override async Task<bool> CanSignInAsync(ApplicationUser user) =>
        !user.IsDisabled && await base.CanSignInAsync(user);
}

/// <summary>Reads the current user from the authentication state (cookie principal or circuit principal).</summary>
public sealed class AuthStateCurrentUser(AuthenticationStateProvider authState) : ICurrentUser
{
    public async Task<string> GetUserIdAsync()
    {
        var user = (await authState.GetAuthenticationStateAsync()).User;
        if (user.Identity?.IsAuthenticated != true) throw new UnauthorizedAccessException("Sign in required.");
        return user.FindFirstValue(ClaimTypes.NameIdentifier) ?? throw new UnauthorizedAccessException("Sign in required.");
    }

    public async Task<bool> IsAdminAsync()
    {
        var user = (await authState.GetAuthenticationStateAsync()).User;
        return user.Identity?.IsAuthenticated == true && user.IsInRole(Roles.Admin);
    }
}

/// <summary>
/// Re-checks every minute that the circuit's user still exists, is not disabled and still has the same
/// security stamp — so disabling an account (which rotates the stamp) ends live sessions quickly.
/// </summary>
public sealed class RevalidatingAuthStateProvider(
    ILoggerFactory loggerFactory,
    IServiceScopeFactory scopeFactory,
    IOptions<IdentityOptions> identityOptions) : RevalidatingServerAuthenticationStateProvider(loggerFactory)
{
    protected override TimeSpan RevalidationInterval => TimeSpan.FromMinutes(1);

    protected override async Task<bool> ValidateAuthenticationStateAsync(AuthenticationState authenticationState, CancellationToken ct)
    {
        await using var scope = scopeFactory.CreateAsyncScope();
        var um = scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
        var principal = authenticationState.User;
        var user = await um.GetUserAsync(principal);
        if (user is null || user.IsDisabled) return false;
        if (!um.SupportsUserSecurityStamp) return true;
        var principalStamp = principal.FindFirstValue(identityOptions.Value.ClaimsIdentity.SecurityStampClaimType);
        return principalStamp == await um.GetSecurityStampAsync(user);
    }
}

/// <summary>Creates accounts (admin bootstrap and invitation registration share this path).</summary>
public sealed class UserProvisioner(
    UserManager<ApplicationUser> userManager,
    RoleManager<IdentityRole> roleManager,
    AppDbContext db,
    TimeProvider clock)
{
    public async Task<IdentityResult> CreateAsync(string email, string username, string password, bool isAdmin, CancellationToken ct = default)
    {
        var ownsTransaction = db.Database.CurrentTransaction is null;
        await using var tx = ownsTransaction ? await db.Database.BeginTransactionAsync(ct) : null;

        var user = new ApplicationUser
        {
            UserName = username.Trim(),
            Email = email.Trim(),
            EmailConfirmed = true, // the address was supplied by the administrator's invitation / bootstrap config
            CreatedAtUtc = clock.GetUtcNow().UtcDateTime,
        };
        var result = await userManager.CreateAsync(user, password);
        if (!result.Succeeded) return result;

        if (isAdmin)
        {
            if (!await roleManager.RoleExistsAsync(Roles.Admin))
                await roleManager.CreateAsync(new IdentityRole(Roles.Admin));
            var roleResult = await userManager.AddToRoleAsync(user, Roles.Admin);
            if (!roleResult.Succeeded) return roleResult;
        }

        await FormulaService.SeedDefaultsAsync(db, user.Id, clock.GetUtcNow().UtcDateTime, ct);
        if (tx is not null) await tx.CommitAsync(ct);
        return result;
    }
}

/// <summary>
/// First-run administrator bootstrap driven by environment variables
/// (ADMIN_EMAIL, ADMIN_USERNAME, ADMIN_PASSWORD, optional DEFAULT_LAB_FORMULA).
/// Runs once: afterwards a marker is stored and the account is never recreated or overwritten.
/// </summary>
public static class AdminBootstrapper
{
    public static async Task RunAsync(IServiceProvider services, IConfiguration config, ILogger logger, CancellationToken ct = default)
    {
        await using var scope = services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<AppDbContext>();
        var users = scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
        var clock = scope.ServiceProvider.GetRequiredService<TimeProvider>();

        if (await db.Settings.AnyAsync(s => s.Key == SettingKeys.AdminBootstrapped, ct)) return;

        if ((await users.GetUsersInRoleAsync(Roles.Admin)).Count > 0)
        {
            await MarkDoneAsync(db, clock, ct); // an administrator already exists; never touch it
            return;
        }

        var email = config["ADMIN_EMAIL"];
        var username = config["ADMIN_USERNAME"];
        var password = config["ADMIN_PASSWORD"];
        if (string.IsNullOrWhiteSpace(email) || string.IsNullOrWhiteSpace(username) || string.IsNullOrWhiteSpace(password))
        {
            logger.LogWarning("No administrator exists yet. Set ADMIN_EMAIL, ADMIN_USERNAME and ADMIN_PASSWORD (and restart) to create the first administrator.");
            return;
        }

        // Optional first-run default lab formula (editable later in Administration).
        var lab = config["DEFAULT_LAB_FORMULA"];
        if (!string.IsNullOrWhiteSpace(lab))
        {
            var error = FormulaService.ValidateExpression(lab, forLabExam: true);
            if (error is null)
            {
                if (!await db.Settings.AnyAsync(s => s.Key == SettingKeys.DefaultLabFormula, ct))
                {
                    db.Settings.Add(new SystemSetting { Key = SettingKeys.DefaultLabFormula, Value = lab.Trim() });
                    await db.SaveChangesAsync(ct);
                }
            }
            else
            {
                logger.LogWarning("DEFAULT_LAB_FORMULA was ignored because it is invalid: {Error}", error);
            }
        }

        var provisioner = scope.ServiceProvider.GetRequiredService<UserProvisioner>();
        var result = await provisioner.CreateAsync(email, username, password, isAdmin: true, ct);
        if (!result.Succeeded)
        {
            // Never log the password. Identity error descriptions do not contain it.
            logger.LogError("Could not create the initial administrator: {Errors}",
                string.Join("; ", result.Errors.Select(e => e.Description)));
            return;
        }

        await MarkDoneAsync(db, clock, ct);
        logger.LogInformation("Initial administrator '{Username}' created.", username);
    }

    private static async Task MarkDoneAsync(AppDbContext db, TimeProvider clock, CancellationToken ct)
    {
        db.Settings.Add(new SystemSetting
        {
            Key = SettingKeys.AdminBootstrapped,
            Value = clock.GetUtcNow().UtcDateTime.ToString("O"),
        });
        await db.SaveChangesAsync(ct);
    }
}
