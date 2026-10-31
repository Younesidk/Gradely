using System.Net.Mail;
using Gradly.Domain.Entities;
using Gradly.Infrastructure.Data;
using Gradly.Infrastructure.Email;
using Gradly.Infrastructure.Identity;
using Microsoft.AspNetCore.Identity;
using Microsoft.EntityFrameworkCore;

namespace Gradly.Application;

/// <summary>An invitation plus the one-time link. The raw token exists only in this object, never in the database.</summary>
public sealed record InvitationIssue(Invitation Invitation, string Url, EmailSendResult? Email);

public sealed record InvitationRow(Invitation Invitation, InvitationStatus Status);

/// <summary>Administrator-only invitation management.</summary>
public sealed class InvitationService(
    IDbContextFactory<AppDbContext> dbFactory,
    ICurrentUser currentUser,
    IEmailService email,
    GradlyOptions options,
    TimeProvider clock)
{
    private DateTime Now => clock.GetUtcNow().UtcDateTime;

    public bool EmailConfigured => email.IsConfigured;

    public async Task<InvitationIssue> CreateAsync(string? address, CancellationToken ct = default)
    {
        await Guard.RequireAdminAsync(currentUser);
        var adminId = await currentUser.GetUserIdAsync();
        var clean = NormalizeEmail(address);

        await using var db = await dbFactory.CreateDbContextAsync(ct);
        var normalized = clean.ToUpperInvariant();
        if (await db.Users.AnyAsync(u => u.NormalizedEmail == normalized, ct))
            throw new UserFacingException("An account with that email already exists.");

        var now = Now;
        if (await db.Invitations.AnyAsync(i => i.NormalizedEmail == normalized && i.RedeemedAtUtc == null
                                               && i.RevokedAtUtc == null && i.ExpiresAtUtc > now, ct))
            throw new UserFacingException("There is already a pending invitation for that address. Regenerate or revoke it instead.");

        var invitation = new Invitation
        {
            Email = clean, NormalizedEmail = normalized, CreatedByUserId = adminId, CreatedAtUtc = now,
        };
        var token = Issue(invitation, now);
        db.Invitations.Add(invitation);
        await db.SaveChangesAsync(ct);
        return await FinishAsync(db, invitation, token, ct);
    }

    /// <summary>Issues a fresh link for a pending or expired invitation (the previous link stops working).</summary>
    public async Task<InvitationIssue> RegenerateAsync(Guid id, CancellationToken ct = default)
    {
        await Guard.RequireAdminAsync(currentUser);
        await using var db = await dbFactory.CreateDbContextAsync(ct);
        var invitation = await db.Invitations.FirstOrDefaultAsync(i => i.Id == id, ct)
                         ?? throw new NotFoundException("That invitation");
        var now = Now;
        var status = invitation.GetStatus(now);
        if (status is InvitationStatus.Redeemed) throw new UserFacingException("This invitation has already been used.");
        if (status is InvitationStatus.Revoked) throw new UserFacingException("This invitation was revoked. Create a new one instead.");

        if (await db.Users.AnyAsync(u => u.NormalizedEmail == invitation.NormalizedEmail, ct))
            throw new UserFacingException("An account with that email already exists.");

        var token = Issue(invitation, now);
        invitation.EmailSentAtUtc = null;
        await db.SaveChangesAsync(ct);
        return await FinishAsync(db, invitation, token, ct);
    }

    public async Task RevokeAsync(Guid id, CancellationToken ct = default)
    {
        await Guard.RequireAdminAsync(currentUser);
        await using var db = await dbFactory.CreateDbContextAsync(ct);
        var invitation = await db.Invitations.FirstOrDefaultAsync(i => i.Id == id, ct)
                         ?? throw new NotFoundException("That invitation");
        if (invitation.GetStatus(Now) != InvitationStatus.Pending)
            throw new UserFacingException("Only pending invitations can be revoked.");
        invitation.RevokedAtUtc = Now;
        await db.SaveChangesAsync(ct);
    }

    public async Task<List<InvitationRow>> ListAsync(CancellationToken ct = default)
    {
        await Guard.RequireAdminAsync(currentUser);
        await using var db = await dbFactory.CreateDbContextAsync(ct);
        var list = await db.Invitations.AsNoTracking().OrderByDescending(i => i.CreatedAtUtc).Take(500).ToListAsync(ct);
        var now = Now;
        return list.Select(i => new InvitationRow(i, i.GetStatus(now))).ToList();
    }

    private string Issue(Invitation invitation, DateTime now)
    {
        var token = TokenHasher.NewToken();
        invitation.TokenHash = TokenHasher.Hash(token);
        invitation.CreatedAtUtc = now;
        invitation.ExpiresAtUtc = now.AddDays(options.InvitationValidDays);
        invitation.RevokedAtUtc = null;
        return token;
    }

    private async Task<InvitationIssue> FinishAsync(AppDbContext db, Invitation invitation, string token, CancellationToken ct)
    {
        var url = BuildUrl(options.BaseUrl, token);
        EmailSendResult? sent = null;
        if (email.IsConfigured)
        {
            sent = await email.SendAsync(EmailTemplates.Invitation(invitation.Email, url, invitation.ExpiresAtUtc), ct);
            if (sent.Success)
            {
                invitation.EmailSentAtUtc = Now;
                await db.SaveChangesAsync(ct);
            }
        }
        return new InvitationIssue(invitation, url, sent);
    }

    public static string BuildUrl(string baseUrl, string token) =>
        $"{baseUrl.TrimEnd('/')}/account/register?token={Uri.EscapeDataString(token)}";

    internal static string NormalizeEmail(string? address)
    {
        var trimmed = address?.Trim() ?? "";
        if (trimmed.Length is 0 or > 256 || !MailAddress.TryCreate(trimmed, out var parsed) || parsed.Address != trimmed)
            throw new UserFacingException("Enter a valid email address.");
        return trimmed;
    }
}

public sealed record RegistrationResult(bool Success, string? Error, string? UserId = null);

/// <summary>Public, invitation-gated account creation. Runs in a single transaction so a token is single-use.</summary>
public sealed class RegistrationService(
    AppDbContext db,
    UserProvisioner provisioner,
    TimeProvider clock)
{
    public const string InvalidInvitation = "This invitation link is invalid, has expired or was already used.";

    /// <summary>Returns the invited address if the token is currently redeemable.</summary>
    public async Task<string?> GetInvitedEmailAsync(string? token, CancellationToken ct = default)
    {
        if (string.IsNullOrWhiteSpace(token) || token.Length > 200) return null;
        var hash = TokenHasher.Hash(token);
        var now = clock.GetUtcNow().UtcDateTime;
        return await db.Invitations.AsNoTracking()
            .Where(i => i.TokenHash == hash && i.RedeemedAtUtc == null && i.RevokedAtUtc == null && i.ExpiresAtUtc > now)
            .Select(i => i.Email).FirstOrDefaultAsync(ct);
    }

    public async Task<RegistrationResult> RegisterAsync(string? token, string? username, string? password, CancellationToken ct = default)
    {
        if (string.IsNullOrWhiteSpace(token) || token.Length > 200) return new(false, InvalidInvitation);
        if (string.IsNullOrWhiteSpace(username) || string.IsNullOrEmpty(password))
            return new(false, "Choose a username and a password.");

        var hash = TokenHasher.Hash(token);
        var now = clock.GetUtcNow().UtcDateTime;

        await using var tx = await db.Database.BeginTransactionAsync(ct);

        var invitation = await db.Invitations.AsNoTracking().FirstOrDefaultAsync(i => i.TokenHash == hash, ct);
        if (invitation is null) return new(false, InvalidInvitation);

        // Atomic claim: only one caller can flip RedeemedAtUtc from NULL.
        var claimed = await db.Invitations
            .Where(i => i.Id == invitation.Id && i.RedeemedAtUtc == null && i.RevokedAtUtc == null && i.ExpiresAtUtc > now)
            .ExecuteUpdateAsync(s => s.SetProperty(i => i.RedeemedAtUtc, now), ct);
        if (claimed != 1) return new(false, InvalidInvitation);

        var result = await provisioner.CreateAsync(invitation.Email, username, password, isAdmin: false, ct);
        if (!result.Succeeded)
        {
            await tx.RollbackAsync(ct); // the invitation stays usable
            return new(false, Describe(result));
        }

        var user = await db.Users.AsNoTracking().FirstAsync(u => u.NormalizedEmail == invitation.NormalizedEmail, ct);
        await db.Invitations.Where(i => i.Id == invitation.Id)
            .ExecuteUpdateAsync(s => s.SetProperty(i => i.RedeemedByUserId, user.Id), ct);
        await tx.CommitAsync(ct);
        return new(true, null, user.Id);
    }

    private static string Describe(IdentityResult r) =>
        string.Join(" ", r.Errors.Select(e => e.Code is "DuplicateEmail" ? "This invitation can't be used." : e.Description));
}
