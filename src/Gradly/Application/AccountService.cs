using System.Net.Mail;
using System.Text;
using Gradly.Domain.Entities;
using Gradly.Infrastructure.Email;
using Microsoft.AspNetCore.Identity;
using Microsoft.AspNetCore.WebUtilities;

namespace Gradly.Application;

public sealed record AccountInfo(string UserName, string Email, bool EmailConfirmed, DateTime CreatedAtUtc, bool IsAdmin);

/// <summary>What happened when a new email address was requested.</summary>
public sealed record EmailChangeRequest(bool EmailSent, string? ManualLink);

/// <summary>Self-service account operations for the signed-in user.</summary>
public sealed class AccountService(
    IServiceScopeFactory scopeFactory,
    ICurrentUser currentUser,
    IEmailService email,
    GradlyOptions options)
{
    public async Task<AccountInfo> GetAsync()
    {
        await using var scope = scopeFactory.CreateAsyncScope();
        var (users, user) = await LoadAsync(scope);
        return new AccountInfo(user.UserName ?? "", user.Email ?? "", user.EmailConfirmed, user.CreatedAtUtc,
            await users.IsInRoleAsync(user, Roles.Admin));
    }

    public async Task ChangeUserNameAsync(string? newName)
    {
        newName = newName?.Trim();
        if (string.IsNullOrEmpty(newName)) throw new UserFacingException("Enter a username.");
        await using var scope = scopeFactory.CreateAsyncScope();
        var (users, user) = await LoadAsync(scope);
        if (string.Equals(user.UserName, newName, StringComparison.Ordinal)) return;
        // UpdateAsync (unlike SetUserNameAsync) keeps the security stamp, so the caller can simply re-issue the cookie.
        user.UserName = newName;
        Throw(await users.UpdateAsync(user));
    }

    /// <summary>
    /// Starts an email change: the address only changes once the link sent to the NEW address is opened.
    /// When email delivery isn't configured (local development) the link is returned so it can be opened manually.
    /// The result never says whether the address already belongs to another account.
    /// </summary>
    public async Task<EmailChangeRequest> RequestEmailChangeAsync(string? newEmail)
    {
        newEmail = newEmail?.Trim();
        if (string.IsNullOrEmpty(newEmail) || newEmail.Length > 256 ||
            !MailAddress.TryCreate(newEmail, out var parsed) || parsed.Address != newEmail)
            throw new UserFacingException("Enter a valid email address.");

        await using var scope = scopeFactory.CreateAsyncScope();
        var (users, user) = await LoadAsync(scope);
        if (string.Equals(user.Email, newEmail, StringComparison.OrdinalIgnoreCase))
            throw new UserFacingException("That is already your email address.");

        var token = await users.GenerateChangeEmailTokenAsync(user, newEmail);
        var code = WebEncoders.Base64UrlEncode(Encoding.UTF8.GetBytes(token));
        var link = $"{options.BaseUrl}/account/confirm-email-change?userId={Uri.EscapeDataString(user.Id)}" +
                   $"&email={Uri.EscapeDataString(newEmail)}&code={code}";

        if (email.IsConfigured)
        {
            var sent = await email.SendAsync(EmailTemplates.ConfirmEmailChange(newEmail, link));
            if (!sent.Success) throw new UserFacingException("We couldn't send the confirmation email. Try again later.");
            return new EmailChangeRequest(true, null);
        }
        return new EmailChangeRequest(false, link);
    }

    public async Task ChangePasswordAsync(string? current, string? next)
    {
        if (string.IsNullOrEmpty(current) || string.IsNullOrEmpty(next))
            throw new UserFacingException("Enter your current and your new password.");
        await using var scope = scopeFactory.CreateAsyncScope();
        var (users, user) = await LoadAsync(scope);
        var result = await users.ChangePasswordAsync(user, current, next);
        if (!result.Succeeded)
            throw new UserFacingException(result.Errors.Any(e => e.Code == "PasswordMismatch")
                ? "Your current password is incorrect."
                : string.Join(" ", result.Errors.Select(e => e.Description)));
    }

    private async Task<(UserManager<ApplicationUser> Users, ApplicationUser User)> LoadAsync(IServiceScope scope)
    {
        var id = await currentUser.GetUserIdAsync();
        var users = scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
        return (users, await users.FindByIdAsync(id) ?? throw new NotFoundException("Your account"));
    }

    private static void Throw(IdentityResult result)
    {
        if (!result.Succeeded)
            throw new UserFacingException(string.Join(" ", result.Errors.Select(e => e.Description)));
    }
}
