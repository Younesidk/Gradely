// Package mail sends transactional email through the Resend HTTP API.
package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"time"
)

type Message struct {
	To, Subject, HTML, Text string
}

// Sender is provider-agnostic: swap the implementation to change provider.
type Sender interface {
	Configured() bool
	Send(ctx context.Context, m Message) error
}

// Resend sends mail through Resend. It does nothing (and says so) when not configured.
type Resend struct {
	APIKey, From string
	Client       *http.Client
	Endpoint     string
}

func NewResend(apiKey, from string) *Resend {
	return &Resend{APIKey: apiKey, From: from, Client: &http.Client{Timeout: 15 * time.Second}, Endpoint: "https://api.resend.com/emails"}
}

func (r *Resend) Configured() bool { return r.APIKey != "" && r.From != "" }

func (r *Resend) Send(ctx context.Context, m Message) error {
	if !r.Configured() {
		return fmt.Errorf("Email delivery isn't configured (RESEND_API_KEY / RESEND_FROM_EMAIL).")
	}
	body, _ := json.Marshal(map[string]any{"from": r.From, "to": []string{m.To}, "subject": m.Subject, "html": m.HTML, "text": m.Text})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.Endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+r.APIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.Client.Do(req)
	if err != nil {
		slog.Warn("sending an email failed")
		return fmt.Errorf("The email provider couldn't be reached.")
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 == 2 {
		return nil
	}
	// Log only the status - never the API key, recipient or message body.
	slog.Warn("resend rejected an email", "status", resp.StatusCode)
	return fmt.Errorf("The email provider rejected the message (HTTP %d).", resp.StatusCode)
}

// Invitation builds the invitation email.
func Invitation(to, url string, expires time.Time) Message {
	date := expires.UTC().Format("January 2, 2006")
	htmlBody := layout("You're invited to Gradely",
		"You've been invited to join <strong>Gradely</strong> — a simple place to keep your semesters, modules and grades, and to see exactly what you need to pass.",
		"Create your account", url,
		fmt.Sprintf("This invitation is for %s and expires on %s (UTC). It can only be used once.", html.EscapeString(to), date))
	text := fmt.Sprintf("You've been invited to join Gradely.\n\nCreate your account: %s\n\nThis invitation is for %s, expires on %s (UTC) and can only be used once.\n\nGradely — Your grades. Your rules.", url, to, date)
	return Message{To: to, Subject: "You're invited to Gradely", HTML: htmlBody, Text: text}
}

// ConfirmEmailChange builds the email-change confirmation email.
func ConfirmEmailChange(to, url string) Message {
	htmlBody := layout("Confirm your new email address",
		"Someone (hopefully you) asked to change the email address on a Gradely account to this one. Confirm to complete the change.",
		"Confirm email address", url, "If you didn't request this, you can safely ignore this message.")
	text := fmt.Sprintf("Confirm your new Gradely email address: %s\n\nIf you didn't request this, ignore this message.", url)
	return Message{To: to, Subject: "Confirm your new Gradely email address", HTML: htmlBody, Text: text}
}

// layout renders a card-style email with inline styles for mail-client compatibility.
func layout(title, body, cta, url, footnote string) string {
	u := html.EscapeString(url)
	return fmt.Sprintf(`<!doctype html>
<html><body style="margin:0;padding:0;background:#f4f4f2;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;color:#1c1c1a;">
  <table role="presentation" width="100%%" cellpadding="0" cellspacing="0" style="background:#f4f4f2;padding:32px 12px;">
    <tr><td align="center">
      <table role="presentation" width="520" cellpadding="0" cellspacing="0" style="max-width:520px;width:100%%;background:#ffffff;border-radius:12px;border:1px solid #e3e3df;">
        <tr><td style="padding:28px 32px 8px 32px;">
          <div style="font-size:22px;font-weight:600;color:#24418a;">Gradely</div>
        </td></tr>
        <tr><td style="padding:16px 32px 0 32px;">
          <h1 style="font-size:20px;line-height:1.3;margin:0 0 12px 0;">%s</h1>
          <p style="font-size:15px;line-height:1.6;margin:0 0 24px 0;color:#4f4f4b;">%s</p>
          <p style="margin:0 0 24px 0;"><a href="%s" style="display:inline-block;background:#24418a;color:#ffffff;text-decoration:none;font-weight:600;font-size:15px;padding:12px 22px;border-radius:8px;">%s</a></p>
          <p style="font-size:12px;line-height:1.6;color:#73736d;margin:0 0 6px 0;">Button not working? Paste this link into your browser:</p>
          <p style="font-size:12px;line-height:1.5;word-break:break-all;margin:0 0 24px 0;"><a href="%s" style="color:#24418a;">%s</a></p>
        </td></tr>
        <tr><td style="padding:16px 32px 28px 32px;border-top:1px solid #ececE8;">
          <p style="font-size:12px;line-height:1.6;color:#73736d;margin:0;">%s</p>
        </td></tr>
      </table>
    </td></tr>
  </table>
</body></html>`, html.EscapeString(title), body, u, html.EscapeString(cta), u, u, footnote)
}
