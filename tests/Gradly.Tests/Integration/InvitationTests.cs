using System.Security.Cryptography;
using System.Text;
using Gradly.Application;
using Gradly.Domain.Entities;
using Microsoft.EntityFrameworkCore;

namespace Gradly.Tests.Integration;

/// <summary>Controllable clock so expiry can be tested without waiting.</summary>
public sealed class FakeClock(DateTimeOffset now) : TimeProvider
{
    public DateTimeOffset Now { get; set; } = now;
    public override DateTimeOffset GetUtcNow() => Now;
}

public class InvitationTests : IClassFixture<SharedFactory>
{
    private readonly GradlyFactory _f;
    public InvitationTests(SharedFactory f) => _f = f;

    private static string TokenFrom(InvitationIssue issue) =>
        Uri.UnescapeDataString(issue.Url[(issue.Url.IndexOf("token=", StringComparison.Ordinal) + 6)..]);

    private static string Email() => $"invitee{Guid.NewGuid():N}"[..20] + "@gradly.test";

    [Fact]
    public async Task Only_the_hash_of_the_token_is_stored_and_link_is_bound_to_the_email()
    {
        var issue = await _f.Invitations(_f.AsAdmin()).CreateAsync(Email());
        var token = TokenFrom(issue);

        Assert.StartsWith("http://gradly.test/account/register?token=", issue.Url);
        Assert.True(token.Length >= 40, "token should carry plenty of entropy");

        await using var db = await _f.Db.CreateDbContextAsync();
        var row = await db.Invitations.AsNoTracking().SingleAsync(i => i.Id == issue.Invitation.Id);
        var expectedHash = Convert.ToHexStringLower(SHA256.HashData(Encoding.UTF8.GetBytes(token)));
        Assert.Equal(expectedHash, row.TokenHash);
        Assert.DoesNotContain(token, row.TokenHash);
        Assert.Equal(issue.Invitation.Email.ToUpperInvariant(), row.NormalizedEmail);
        Assert.Equal(7, (row.ExpiresAtUtc - row.CreatedAtUtc).TotalDays, 3);
    }

    [Fact]
    public async Task Tokens_are_unique_and_random()
    {
        var svc = _f.Invitations(_f.AsAdmin());
        var tokens = new HashSet<string>();
        for (var i = 0; i < 20; i++) tokens.Add(TokenFrom(await svc.CreateAsync(Email())));
        Assert.Equal(20, tokens.Count);
    }

    [Fact]
    public async Task Registration_creates_a_user_with_the_invited_email_and_redeems_once()
    {
        var email = Email();
        var issue = await _f.Invitations(_f.AsAdmin()).CreateAsync(email);
        var token = TokenFrom(issue);
        var username = "newbie" + Guid.NewGuid().ToString("N")[..6];

        var ok = await _f.RegisterAsync(token, username);
        Assert.True(ok.Success, ok.Error);

        var user = await _f.FindUserAsync(username);
        Assert.Equal(email, user.Email);          // email comes from the invitation, never from the form
        Assert.True(user.EmailConfirmed);
        Assert.False(user.IsDisabled);

        // new users get their starter formula
        var formulas = await _f.Formulas(GradlyFactory.As(user)).ListAsync();
        Assert.Contains(formulas, f => f.Formula.Expression == "(CC1 + CC2) / 2 * 0.4 + Exam * 0.6" && !f.Formula.ForLabExam);

        // second use of the same token fails and creates nothing
        var again = await _f.RegisterAsync(token, "second" + username);
        Assert.False(again.Success);
        Assert.Equal(RegistrationService.InvalidInvitation, again.Error);
        Assert.Null(await _f.FindUserAsync("second" + username));

        await using var db = await _f.Db.CreateDbContextAsync();
        var row = await db.Invitations.AsNoTracking().SingleAsync(i => i.Id == issue.Invitation.Id);
        Assert.NotNull(row.RedeemedAtUtc);
        Assert.Equal(user.Id, row.RedeemedByUserId);
        Assert.Equal(InvitationStatus.Redeemed, row.GetStatus(DateTime.UtcNow));
    }

    [Fact]
    public async Task Concurrent_redemptions_create_exactly_one_account()
    {
        var issue = await _f.Invitations(_f.AsAdmin()).CreateAsync(Email());
        var token = TokenFrom(issue);
        var results = await Task.WhenAll(Enumerable.Range(0, 4)
            .Select(i => _f.RegisterAsync(token, "racer" + i + Guid.NewGuid().ToString("N")[..5])));
        Assert.Equal(1, results.Count(r => r.Success));
    }

    [Fact]
    public async Task Expired_invitation_cannot_be_redeemed()
    {
        var clock = new FakeClock(DateTimeOffset.UtcNow.AddDays(-8)); // issued 8 days ago -> expired 1 day ago
        var issue = await _f.Invitations(_f.AsAdmin(), clock).CreateAsync(Email());
        var token = TokenFrom(issue);

        var res = await _f.RegisterAsync(token, "late" + Guid.NewGuid().ToString("N")[..6]);
        Assert.False(res.Success);
        Assert.Equal(RegistrationService.InvalidInvitation, res.Error);

        var rows = await _f.Invitations(_f.AsAdmin()).ListAsync();
        Assert.Equal(InvitationStatus.Expired, rows.Single(r => r.Invitation.Id == issue.Invitation.Id).Status);
    }

    [Fact]
    public async Task Invitation_just_inside_its_validity_window_still_works()
    {
        var clock = new FakeClock(DateTimeOffset.UtcNow.AddDays(-6).AddHours(-23));
        var issue = await _f.Invitations(_f.AsAdmin(), clock).CreateAsync(Email());
        var res = await _f.RegisterAsync(TokenFrom(issue), "edge" + Guid.NewGuid().ToString("N")[..6]);
        Assert.True(res.Success, res.Error);
    }

    [Fact]
    public async Task Expired_invitation_can_be_regenerated_and_old_link_stays_dead()
    {
        var clock = new FakeClock(DateTimeOffset.UtcNow.AddDays(-10));
        var admin = _f.AsAdmin();
        var issue = await _f.Invitations(admin, clock).CreateAsync(Email());
        var oldToken = TokenFrom(issue);

        var renewed = await _f.Invitations(admin).RegenerateAsync(issue.Invitation.Id);
        var newToken = TokenFrom(renewed);
        Assert.NotEqual(oldToken, newToken);
        Assert.Equal(issue.Invitation.Id, renewed.Invitation.Id);

        Assert.False((await _f.RegisterAsync(oldToken, "old" + Guid.NewGuid().ToString("N")[..6])).Success);
        Assert.True((await _f.RegisterAsync(newToken, "new" + Guid.NewGuid().ToString("N")[..6])).Success);
    }

    [Fact]
    public async Task Revoked_invitation_cannot_be_redeemed_and_only_pending_can_be_revoked()
    {
        var svc = _f.Invitations(_f.AsAdmin());
        var issue = await svc.CreateAsync(Email());
        await svc.RevokeAsync(issue.Invitation.Id);

        Assert.False((await _f.RegisterAsync(TokenFrom(issue), "rev" + Guid.NewGuid().ToString("N")[..6])).Success);
        await Assert.ThrowsAsync<UserFacingException>(() => svc.RevokeAsync(issue.Invitation.Id));
        await Assert.ThrowsAsync<UserFacingException>(() => svc.RegenerateAsync(issue.Invitation.Id));
    }

    [Fact]
    public async Task Redeemed_invitation_cannot_be_regenerated()
    {
        var svc = _f.Invitations(_f.AsAdmin());
        var issue = await svc.CreateAsync(Email());
        await _f.RegisterAsync(TokenFrom(issue), "done" + Guid.NewGuid().ToString("N")[..6]);
        await Assert.ThrowsAsync<UserFacingException>(() => svc.RegenerateAsync(issue.Invitation.Id));
    }

    [Fact]
    public async Task Failed_registration_does_not_burn_the_invitation()
    {
        var issue = await _f.Invitations(_f.AsAdmin()).CreateAsync(Email());
        var token = TokenFrom(issue);

        var weak = await _f.RegisterAsync(token, "weakling" + Guid.NewGuid().ToString("N")[..4], "short");
        Assert.False(weak.Success);

        // same token still works with a valid password
        var ok = await _f.RegisterAsync(token, "strong" + Guid.NewGuid().ToString("N")[..6]);
        Assert.True(ok.Success, ok.Error);
    }

    [Fact]
    public async Task Taken_username_is_rejected_without_burning_the_invitation()
    {
        await _f.CreateUserAsync("takenname");
        var issue = await _f.Invitations(_f.AsAdmin()).CreateAsync(Email());
        var token = TokenFrom(issue);
        Assert.False((await _f.RegisterAsync(token, "takenname")).Success);
        Assert.True((await _f.RegisterAsync(token, "freename" + Guid.NewGuid().ToString("N")[..4])).Success);
    }

    [Fact]
    public async Task Garbage_tokens_are_rejected()
    {
        foreach (var t in new[] { null, "", "   ", "not-a-token", new string('A', 500) })
        {
            var res = await _f.RegisterAsync(t, "x" + Guid.NewGuid().ToString("N")[..6]);
            Assert.False(res.Success);
        }
    }

    [Fact]
    public async Task Cannot_invite_an_existing_user_or_duplicate_a_pending_invitation()
    {
        var existing = await _f.CreateUserAsync("existing" + Guid.NewGuid().ToString("N")[..4]);
        var svc = _f.Invitations(_f.AsAdmin());
        await Assert.ThrowsAsync<UserFacingException>(() => svc.CreateAsync(existing.Email));

        var email = Email();
        await svc.CreateAsync(email);
        await Assert.ThrowsAsync<UserFacingException>(() => svc.CreateAsync(email));
        await Assert.ThrowsAsync<UserFacingException>(() => svc.CreateAsync(email.ToUpperInvariant()));
    }

    [Theory]
    [InlineData("")]
    [InlineData("not-an-email")]
    [InlineData("a@")]
    [InlineData("Name <a@b.com>")]
    public async Task Invalid_email_addresses_are_rejected(string email) =>
        await Assert.ThrowsAsync<UserFacingException>(() => _f.Invitations(_f.AsAdmin()).CreateAsync(email));

    [Fact]
    public async Task Invitation_email_is_sent_when_configured_and_link_is_still_returned()
    {
        using var f = new GradlyFactory();
        f.Email.IsConfigured = true;
        var email = Email();
        var issue = await f.Invitations(f.AsAdmin()).CreateAsync(email);

        Assert.True(issue.Email!.Success);
        var sent = Assert.Single(f.Email.Sent);
        Assert.Equal(email, sent.To);
        Assert.Contains(issue.Url, sent.Html.Replace("&amp;", "&"));
        Assert.Contains(issue.Url, sent.Text);
        Assert.Contains("Gradly", sent.Subject);
        Assert.NotNull(issue.Invitation.EmailSentAtUtc);
    }

    [Fact]
    public async Task Email_failure_or_missing_configuration_falls_back_to_a_copyable_link()
    {
        using var f = new GradlyFactory();

        // not configured: no send attempted, link available
        var a = await f.Invitations(f.AsAdmin()).CreateAsync(Email());
        Assert.Null(a.Email);
        Assert.Empty(f.Email.Sent);
        Assert.Contains("/account/register?token=", a.Url);
        Assert.Null(a.Invitation.EmailSentAtUtc);

        // configured but the provider is down: failure is reported, link still available, invitation kept
        f.Email.IsConfigured = true;
        f.Email.Fail = true;
        var b = await f.Invitations(f.AsAdmin()).CreateAsync(Email());
        Assert.False(b.Email!.Success);
        Assert.Contains("/account/register?token=", b.Url);
        Assert.Null(b.Invitation.EmailSentAtUtc);
        Assert.True((await f.RegisterAsync(Uri.UnescapeDataString(b.Url.Split("token=")[1]), "viacopy" + Guid.NewGuid().ToString("N")[..4])).Success);
    }

    [Fact]
    public async Task End_to_end_registration_through_the_web_form()
    {
        var email = Email();
        var issue = await _f.Invitations(_f.AsAdmin()).CreateAsync(email);
        var token = Uri.EscapeDataString(TokenFrom(issue));
        var username = "webuser" + Guid.NewGuid().ToString("N")[..5];
        var client = _f.NewClient();

        var page = await (await client.GetAsync($"/account/register?token={token}")).Content.ReadAsStringAsync();
        Assert.Contains(email, page);
        Assert.Contains("id=\"username\"", page);

        var res = await client.PostFormAsync($"/account/register?token={token}", new Dictionary<string, string>
        {
            ["Input.UserName"] = username,
            ["Input.Password"] = GradlyFactory.UserPassword,
            ["Input.ConfirmPassword"] = GradlyFactory.UserPassword,
        });
        Assert.True(res.IsRedirectTo("/"), res.StatusCode + " " + res.Headers.Location);
        Assert.Equal(System.Net.HttpStatusCode.OK, (await client.GetAsync("/semesters")).StatusCode); // signed in
        Assert.True((await _f.NewClient().SignInAsync(username, GradlyFactory.UserPassword)).IsRedirectTo("/"));

        // the page for the used token now says invalid
        var after = await (await _f.NewClient().GetAsync($"/account/register?token={token}")).Content.ReadAsStringAsync();
        Assert.DoesNotContain("id=\"username\"", after);
    }
}
