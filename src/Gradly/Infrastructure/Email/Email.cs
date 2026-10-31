using System.Net;
using System.Net.Http.Headers;
using System.Net.Http.Json;
using Gradly.Application;

namespace Gradly.Infrastructure.Email;

public sealed record EmailMessage(string To, string Subject, string Html, string Text);

public sealed record EmailSendResult(bool Success, string? Error)
{
    public static EmailSendResult Ok() => new(true, null);
    public static EmailSendResult Fail(string error) => new(false, error);
}

/// <summary>Provider-agnostic email sending. Swap the implementation to change provider.</summary>
public interface IEmailService
{
    bool IsConfigured { get; }
    Task<EmailSendResult> SendAsync(EmailMessage message, CancellationToken ct = default);
}

/// <summary>Sends mail through the Resend HTTP API. Does nothing (and says so) when not configured.</summary>
public sealed class ResendEmailService(HttpClient http, GradlyOptions options, ILogger<ResendEmailService> logger) : IEmailService
{
    public bool IsConfigured => options.EmailConfigured;

    public async Task<EmailSendResult> SendAsync(EmailMessage message, CancellationToken ct = default)
    {
        if (!IsConfigured) return EmailSendResult.Fail("Email delivery isn't configured (RESEND_API_KEY / RESEND_FROM_EMAIL).");

        try
        {
            using var request = new HttpRequestMessage(HttpMethod.Post, "emails")
            {
                Content = JsonContent.Create(new
                {
                    from = options.ResendFromEmail,
                    to = new[] { message.To },
                    subject = message.Subject,
                    html = message.Html,
                    text = message.Text,
                }),
            };
            request.Headers.Authorization = new AuthenticationHeaderValue("Bearer", options.ResendApiKey);
            using var response = await http.SendAsync(request, ct);
            if (response.IsSuccessStatusCode) return EmailSendResult.Ok();

            // Log only the status — never the API key, recipient, or message body.
            logger.LogWarning("Resend rejected an email with HTTP {Status}.", (int)response.StatusCode);
            return EmailSendResult.Fail($"The email provider rejected the message (HTTP {(int)response.StatusCode}).");
        }
        catch (Exception ex) when (ex is HttpRequestException or TaskCanceledException)
        {
            logger.LogWarning("Sending an email failed: {Type}.", ex.GetType().Name);
            return EmailSendResult.Fail("The email provider couldn't be reached.");
        }
    }
}

/// <summary>Branded Gradly email templates (inline styles for mail-client compatibility).</summary>
public static class EmailTemplates
{
    public static EmailMessage Invitation(string to, string url, DateTime expiresUtc)
    {
        var expires = expiresUtc.ToString("MMMM d, yyyy", System.Globalization.CultureInfo.InvariantCulture);
        var html = Layout("You're invited to Gradly",
            "You've been invited to join <strong>Gradly</strong> — a simple place to keep your semesters, modules and grades, and to see exactly what you need to pass.",
            "Create your account", url,
            $"This invitation is for {WebUtility.HtmlEncode(to)} and expires on {expires} (UTC). It can only be used once.");
        var text = $"You've been invited to join Gradly.\n\nCreate your account: {url}\n\nThis invitation is for {to}, expires on {expires} (UTC) and can only be used once.\n\nGradly — Your grades. Your rules.";
        return new EmailMessage(to, "You're invited to Gradly", html, text);
    }

    public static EmailMessage ConfirmEmailChange(string to, string url)
    {
        var html = Layout("Confirm your new email address",
            "Someone (hopefully you) asked to change the email address on a Gradly account to this one. Confirm to complete the change.",
            "Confirm email address", url,
            "If you didn't request this, you can safely ignore this message.");
        var text = $"Confirm your new Gradly email address: {url}\n\nIf you didn't request this, ignore this message.";
        return new EmailMessage(to, "Confirm your new Gradly email address", html, text);
    }

    private static string Layout(string title, string body, string cta, string url, string footnote)
    {
        var safeUrl = WebUtility.HtmlEncode(url);
        return $$"""
        <!doctype html>
        <html><body style="margin:0;padding:0;background:#f4f5f9;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;color:#1c2033;">
          <table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#f4f5f9;padding:32px 12px;">
            <tr><td align="center">
              <table role="presentation" width="520" cellpadding="0" cellspacing="0" style="max-width:520px;width:100%;background:#ffffff;border-radius:14px;border:1px solid #e3e6ef;">
                <tr><td style="padding:28px 32px 8px 32px;">
                  <div style="font-size:22px;font-weight:700;letter-spacing:-0.02em;color:#4f46e5;">Gradly</div>
                  <div style="font-size:12px;color:#7a8099;margin-top:2px;">Your grades. Your rules.</div>
                </td></tr>
                <tr><td style="padding:16px 32px 0 32px;">
                  <h1 style="font-size:20px;line-height:1.3;margin:0 0 12px 0;">{{WebUtility.HtmlEncode(title)}}</h1>
                  <p style="font-size:15px;line-height:1.6;margin:0 0 24px 0;color:#3d4360;">{{body}}</p>
                  <p style="margin:0 0 24px 0;"><a href="{{safeUrl}}" style="display:inline-block;background:#4f46e5;color:#ffffff;text-decoration:none;font-weight:600;font-size:15px;padding:12px 22px;border-radius:10px;">{{WebUtility.HtmlEncode(cta)}}</a></p>
                  <p style="font-size:12px;line-height:1.6;color:#7a8099;margin:0 0 6px 0;">Button not working? Paste this link into your browser:</p>
                  <p style="font-size:12px;line-height:1.5;word-break:break-all;margin:0 0 24px 0;"><a href="{{safeUrl}}" style="color:#4f46e5;">{{safeUrl}}</a></p>
                </td></tr>
                <tr><td style="padding:16px 32px 28px 32px;border-top:1px solid #eceef5;">
                  <p style="font-size:12px;line-height:1.6;color:#7a8099;margin:0;">{{footnote}}</p>
                </td></tr>
              </table>
            </td></tr>
          </table>
        </body></html>
        """;
    }
}
