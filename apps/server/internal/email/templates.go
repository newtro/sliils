package email

import (
	"bytes"
	"fmt"
	"html/template"
	"strings"
)

// Template builders produce ready-to-send Messages for each auth flow.
// Keep the HTML tight, inline-styled, and testable. No external assets.

type baseVars struct {
	BrandName string
	Heading   string
	Preheader string
	Intro     string
	ButtonURL string
	ButtonLbl string
	Body2     string
	Footer    string
}

var baseHTML = template.Must(template.New("base").Parse(strings.TrimSpace(`
<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8" />
<title>{{.BrandName}} — {{.Heading}}</title>
</head>
<body style="margin:0;padding:0;background:#fbfbfd;font-family:-apple-system,Segoe UI,Roboto,sans-serif;color:#1A2D43;">
<span style="display:none !important;opacity:0;color:transparent;visibility:hidden;">{{.Preheader}}</span>
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="background:#fbfbfd;padding:32px 0;">
  <tr><td align="center">
    <table role="presentation" width="560" cellpadding="0" cellspacing="0" border="0" style="background:#ffffff;border:1px solid rgba(26,45,67,0.08);border-radius:12px;max-width:560px;">
      <tr><td style="padding:32px 40px;">
        <div style="font-size:24px;font-weight:700;letter-spacing:-0.02em;color:#1A2D43;margin-bottom:4px;">
          Sl<span style="color:#5BB85C;">i</span><span style="color:#3B7DD8;">i</span>lS
        </div>
        <h1 style="margin:16px 0 8px;font-size:20px;color:#1A2D43;">{{.Heading}}</h1>
        <p style="margin:0 0 16px;font-size:15px;line-height:1.55;color:#1A2D43;">{{.Intro}}</p>
        {{if .ButtonURL}}
        <div style="margin:24px 0;">
          <a href="{{.ButtonURL}}"
             style="display:inline-block;padding:12px 24px;background:#3B7DD8;color:#ffffff;text-decoration:none;border-radius:8px;font-weight:600;font-size:15px;">
            {{.ButtonLbl}}
          </a>
        </div>
        <p style="margin:0 0 16px;font-size:13px;color:#6B7A8F;word-break:break-all;">
          Or paste this link in your browser:<br/>
          <a href="{{.ButtonURL}}" style="color:#3B7DD8;">{{.ButtonURL}}</a>
        </p>
        {{end}}
        {{if .Body2}}
        <p style="margin:16px 0 0;font-size:14px;color:#6B7A8F;">{{.Body2}}</p>
        {{end}}
        <hr style="border:none;border-top:1px solid rgba(26,45,67,0.08);margin:24px 0;" />
        <p style="margin:0;font-size:12px;color:#6B7A8F;">{{.Footer}}</p>
      </td></tr>
    </table>
  </td></tr>
</table>
</body>
</html>
`)))

func render(vars baseVars) (htmlBody, textBody string) {
	var buf bytes.Buffer
	_ = baseHTML.Execute(&buf, vars)

	var txt strings.Builder
	txt.WriteString(vars.Heading)
	txt.WriteString("\n\n")
	txt.WriteString(vars.Intro)
	txt.WriteString("\n")
	if vars.ButtonURL != "" {
		txt.WriteString("\n")
		txt.WriteString(vars.ButtonLbl)
		txt.WriteString(": ")
		txt.WriteString(vars.ButtonURL)
		txt.WriteString("\n")
	}
	if vars.Body2 != "" {
		txt.WriteString("\n")
		txt.WriteString(vars.Body2)
		txt.WriteString("\n")
	}
	txt.WriteString("\n—\n")
	txt.WriteString(vars.Footer)
	txt.WriteString("\n")

	return buf.String(), txt.String()
}

// VerifyEmail builds the email-verification message.
func VerifyEmail(recipient, verifyURL string) Message {
	htmlBody, textBody := render(baseVars{
		BrandName: "SliilS",
		Heading:   "Verify your email",
		Preheader: "Confirm your SliilS email to finish signing up.",
		Intro:     "Click the button below to verify your email and activate your SliilS account.",
		ButtonURL: verifyURL,
		ButtonLbl: "Verify email",
		Body2:     "If you didn't sign up for SliilS, you can ignore this message.",
		Footer:    "This link expires in 24 hours. SliilS · self-hosted team collaboration.",
	})
	return Message{
		To:       []string{recipient},
		Subject:  "Verify your SliilS email",
		HTMLBody: htmlBody,
		TextBody: textBody,
		Tags:     map[string]string{"purpose": "email_verify"},
	}
}

// MagicLink builds the magic-link sign-in message.
func MagicLink(recipient, loginURL string) Message {
	htmlBody, textBody := render(baseVars{
		BrandName: "SliilS",
		Heading:   "Your SliilS sign-in link",
		Preheader: "Tap to sign in to SliilS — no password needed.",
		Intro:     "Here's your one-time sign-in link. It expires in 15 minutes and can only be used once.",
		ButtonURL: loginURL,
		ButtonLbl: "Sign in to SliilS",
		Body2:     "If you didn't request this, you can ignore it — nothing has happened to your account.",
		Footer:    fmt.Sprintf("You requested this from SliilS. If this wasn't you, check your account for suspicious activity."),
	})
	return Message{
		To:       []string{recipient},
		Subject:  "Your SliilS sign-in link",
		HTMLBody: htmlBody,
		TextBody: textBody,
		Tags:     map[string]string{"purpose": "magic_link"},
	}
}

// WorkspaceInvite builds the "you've been invited to a workspace" message.
// inviter is the display name / email of the admin who sent the invite;
// workspaceName is the human-friendly name shown in the body.
func WorkspaceInvite(recipient, workspaceName, inviter, acceptURL string) Message {
	heading := fmt.Sprintf("You've been invited to %s on SliilS", workspaceName)
	intro := fmt.Sprintf("%s invited you to join %s — a SliilS workspace.", inviter, workspaceName)
	htmlBody, textBody := render(baseVars{
		BrandName: "SliilS",
		Heading:   heading,
		Preheader: fmt.Sprintf("Join %s on SliilS.", workspaceName),
		Intro:     intro,
		ButtonURL: acceptURL,
		ButtonLbl: "Accept invite",
		Body2:     "You'll be signed in (or asked to sign up) first, then added to the workspace automatically. Invite links expire in 7 days.",
		Footer:    "SliilS · self-hosted team collaboration.",
	})
	return Message{
		To:       []string{recipient},
		Subject:  fmt.Sprintf("Invitation to join %s on SliilS", workspaceName),
		HTMLBody: htmlBody,
		TextBody: textBody,
		Tags:     map[string]string{"purpose": "workspace_invite"},
	}
}

// PasswordReset builds the password-reset message.
func PasswordReset(recipient, resetURL string) Message {
	htmlBody, textBody := render(baseVars{
		BrandName: "SliilS",
		Heading:   "Reset your SliilS password",
		Preheader: "Use this link to set a new password.",
		Intro:     "Click below to set a new password for your SliilS account. The link is valid for 1 hour.",
		ButtonURL: resetURL,
		ButtonLbl: "Reset password",
		Body2:     "If you didn't ask to reset your password, you can ignore this — your current password still works.",
		Footer:    "SliilS · self-hosted team collaboration.",
	})
	return Message{
		To:       []string{recipient},
		Subject:  "Reset your SliilS password",
		HTMLBody: htmlBody,
		TextBody: textBody,
		Tags:     map[string]string{"purpose": "password_reset"},
	}
}
