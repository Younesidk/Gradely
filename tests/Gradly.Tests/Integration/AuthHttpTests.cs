using System.Net;
using Gradly.Application;

namespace Gradly.Tests.Integration;

/// <summary>End-to-end HTTP checks of sign-in, route protection, disabling and rate limiting.</summary>
public class AuthHttpTests : IClassFixture<SharedFactory>
{
    private readonly GradlyFactory _f;
    public AuthHttpTests(SharedFactory f) => _f = f;

    [Theory]
    [InlineData("/")]
    [InlineData("/semesters")]
    [InlineData("/formulas")]
    [InlineData("/settings")]
    [InlineData("/admin")]
    [InlineData("/admin/users")]
    [InlineData("/admin/invitations")]
    public async Task Anonymous_visitors_are_sent_to_login(string path)
    {
        var res = await _f.NewClient().GetAsync(path);
        Assert.True(res.IsRedirectTo("/account/login"), $"{path} -> {(int)res.StatusCode} {res.Headers.Location}");
    }

    [Fact]
    public async Task Login_page_is_public_and_there_is_no_open_registration()
    {
        var client = _f.NewClient();
        Assert.Equal(HttpStatusCode.OK, (await client.GetAsync("/account/login")).StatusCode);

        // Registration without a token (or with a bogus one) shows no form.
        foreach (var url in new[] { "/account/register", "/account/register?token=bogus", "/account/register?token=" })
        {
            var html = await (await client.GetAsync(url)).Content.ReadAsStringAsync();
            Assert.DoesNotContain("id=\"username\"", html);
            Assert.Contains("invalid", html, StringComparison.OrdinalIgnoreCase);
        }
        // Posting to register without a valid token creates nothing.
        var res = await client.PostFormAsync("/account/register?token=bogus", new Dictionary<string, string>
        {
            ["Input.UserName"] = "mallory", ["Input.Password"] = "Mallory-Pass-1", ["Input.ConfirmPassword"] = "Mallory-Pass-1",
        });
        Assert.NotEqual(HttpStatusCode.Redirect, res.StatusCode);
        Assert.Null(await _f.FindUserAsync("mallory"));
    }

    [Fact]
    public async Task Admin_can_sign_in_and_reach_admin_area()
    {
        var client = _f.NewClient();
        var login = await client.SignInAsync("admin", GradlyFactory.AdminPassword);
        Assert.True(login.IsRedirectTo("/"), login.StatusCode + " " + login.Headers.Location);
        Assert.Equal(HttpStatusCode.OK, (await client.GetAsync("/admin")).StatusCode);
        Assert.Equal(HttpStatusCode.OK, (await client.GetAsync("/admin/users")).StatusCode);
        Assert.Equal(HttpStatusCode.OK, (await client.GetAsync("/semesters")).StatusCode);
    }

    [Fact]
    public async Task Login_works_with_username_or_email_and_fails_generically()
    {
        await _f.CreateUserAsync("carol", "carol@gradly.test");
        Assert.True((await _f.NewClient().SignInAsync("carol", GradlyFactory.UserPassword)).IsRedirectTo("/"));
        Assert.True((await _f.NewClient().SignInAsync("carol@gradly.test", GradlyFactory.UserPassword)).IsRedirectTo("/"));

        // Wrong password and unknown account give the same response body (no account enumeration).
        var wrongPw = await (await _f.NewClient().SignInAsync("carol", "nope-nope-123")).Content.ReadAsStringAsync();
        var unknown = await (await _f.NewClient().SignInAsync("nobody-here", "nope-nope-123")).Content.ReadAsStringAsync();
        Assert.Contains("Sign-in failed", wrongPw);
        Assert.Contains("Sign-in failed", unknown);
        Assert.DoesNotContain("carol@gradly.test", wrongPw);
        Assert.False(string.IsNullOrEmpty(StripTokens(wrongPw)));
        Assert.Equal(StripTokens(wrongPw), StripTokens(unknown));
    }

    /// <summary>The visible error message only (page chrome contains per-request antiforgery tokens).</summary>
    private static string StripTokens(string html) =>
        System.Text.RegularExpressions.Regex.Match(html, "<div class=\"alert alert-danger\"[^>]*>(.*?)</div>",
            System.Text.RegularExpressions.RegexOptions.Singleline).Groups[1].Value.Trim();

    [Fact]
    public async Task Normal_user_cannot_open_admin_pages_but_can_use_the_app()
    {
        await _f.CreateUserAsync("dave");
        var client = _f.NewClient();
        Assert.True((await client.SignInAsync("dave", GradlyFactory.UserPassword)).IsRedirectTo("/"));

        foreach (var path in new[] { "/admin", "/admin/users", "/admin/invitations" })
        {
            var res = await client.GetAsync(path);
            Assert.True(res.IsRedirectTo("/account/access-denied"), $"{path} -> {(int)res.StatusCode} {res.Headers.Location}");
        }
        Assert.Equal(HttpStatusCode.OK, (await client.GetAsync("/semesters")).StatusCode);
        Assert.Equal(HttpStatusCode.OK, (await client.GetAsync("/")).StatusCode);

        // The admin navigation is not rendered for normal users.
        var home = await (await client.GetAsync("/")).Content.ReadAsStringAsync();
        Assert.DoesNotContain("Administration", home);
        Assert.DoesNotContain("/admin/invitations", home);
    }

    [Fact]
    public async Task Disabled_user_cannot_sign_in_and_existing_session_is_revoked_then_restored_on_enable()
    {
        var erin = await _f.CreateUserAsync("erin");
        var userClient = _f.NewClient();
        Assert.True((await userClient.SignInAsync("erin", GradlyFactory.UserPassword)).IsRedirectTo("/"));
        Assert.Equal(HttpStatusCode.OK, (await userClient.GetAsync("/semesters")).StatusCode);

        var admin = _f.AdminSvc(_f.AsAdmin());
        await admin.SetDisabledAsync(erin.Id, true);

        // existing cookie no longer valid
        Assert.True((await userClient.GetAsync("/semesters")).IsRedirectTo("/account/login"));
        // fresh login refused, with the correct password
        var attempt = await _f.NewClient().SignInAsync("erin", GradlyFactory.UserPassword);
        Assert.Equal(HttpStatusCode.OK, attempt.StatusCode); // login form re-rendered, no redirect
        Assert.Contains("Sign-in failed", await attempt.Content.ReadAsStringAsync());

        // data is preserved while disabled
        var detail = await admin.GetUserAsync(erin.Id);
        Assert.True(detail.Row.IsDisabled);

        await admin.SetDisabledAsync(erin.Id, false);
        Assert.True((await _f.NewClient().SignInAsync("erin", GradlyFactory.UserPassword)).IsRedirectTo("/"));
    }

    [Fact]
    public async Task Logout_requires_an_antiforgery_token_and_clears_the_session()
    {
        await _f.CreateUserAsync("frank");
        var client = _f.NewClient();
        await client.SignInAsync("frank", GradlyFactory.UserPassword);

        var forged = await client.PostAsync("/account/logout", new FormUrlEncodedContent(new Dictionary<string, string>()));
        Assert.Equal(HttpStatusCode.BadRequest, forged.StatusCode);
        Assert.Equal(HttpStatusCode.OK, (await client.GetAsync("/semesters")).StatusCode); // still signed in

        // The real sign-out form (rendered inside the app layout) carries a valid token.
        var res = await client.PostFormAsync("/account/logout", new Dictionary<string, string>(), pageUrl: "/settings");
        Assert.True(res.IsRedirectTo("/account/login"), res.StatusCode + " " + res.Headers.Location);
        Assert.True((await client.GetAsync("/semesters")).IsRedirectTo("/account/login"));
    }

    [Theory]
    [InlineData("//evil.example/steal", "/")]
    [InlineData("https://evil.example", "/")]
    [InlineData("/\\evil.example", "/")]
    [InlineData("/semesters", "/semesters")]
    [InlineData(null, "/")]
    public void Return_urls_must_be_local(string? input, string expected) =>
        Assert.Equal(expected, LocalUrl.Safe(input));

    [Fact]
    public async Task Login_redirect_ignores_external_return_url()
    {
        await _f.CreateUserAsync("gina");
        var res = await _f.NewClient().PostFormAsync("/account/login?returnUrl=%2F%2Fevil.example", new Dictionary<string, string>
        {
            ["Input.Identifier"] = "gina", ["Input.Password"] = GradlyFactory.UserPassword,
        });
        Assert.True(res.IsRedirectTo("/"), res.Headers.Location?.ToString());
        Assert.DoesNotContain("evil.example", res.Headers.Location!.ToString());
    }

    [Fact]
    public async Task Protected_page_redirect_carries_a_return_url_that_login_honours()
    {
        await _f.CreateUserAsync("ivy");
        var client = _f.NewClient();
        var redirect = await client.GetAsync("/semesters");
        Assert.True(redirect.IsRedirectTo("/account/login"));
        var location = redirect.Headers.Location!.OriginalString;
        Assert.Contains("ReturnUrl=%2Fsemesters", location);

        var res = await client.PostFormAsync(location, new Dictionary<string, string>
        {
            ["Input.Identifier"] = "ivy", ["Input.Password"] = GradlyFactory.UserPassword,
        });
        Assert.True(res.IsRedirectTo("/semesters"), res.Headers.Location?.ToString());
    }

    [Fact]
    public async Task Repeated_failed_logins_lock_the_account()
    {
        await _f.CreateUserAsync("harry");
        for (var i = 0; i < 5; i++) await _f.NewClient().SignInAsync("harry", "wrong-password-1");
        // now even the right password is refused
        var res = await _f.NewClient().SignInAsync("harry", GradlyFactory.UserPassword);
        Assert.Equal(HttpStatusCode.OK, res.StatusCode);
        Assert.Contains("Sign-in failed", await res.Content.ReadAsStringAsync());
    }

    [Fact]
    public async Task Responses_carry_basic_security_headers()
    {
        var res = await _f.NewClient().GetAsync("/account/login");
        Assert.Equal("nosniff", res.Headers.GetValues("X-Content-Type-Options").Single());
        Assert.Equal("DENY", res.Headers.GetValues("X-Frame-Options").Single());
    }
}

public class RateLimitTests
{
    [Fact]
    public async Task Auth_posts_are_rate_limited_per_client()
    {
        using var f = new GradlyFactory(rateLimit: 3);
        var client = f.NewClient();
        var codes = new List<HttpStatusCode>();
        for (var i = 0; i < 6; i++)
            codes.Add((await client.PostAsync("/account/login", new FormUrlEncodedContent(new Dictionary<string, string>()))).StatusCode);
        Assert.Contains(HttpStatusCode.TooManyRequests, codes);
        Assert.NotEqual(HttpStatusCode.TooManyRequests, codes[0]);

        // Ordinary pages stay reachable.
        Assert.Equal(HttpStatusCode.OK, (await client.GetAsync("/account/login")).StatusCode);
    }
}
