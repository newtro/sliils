package email

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	resend "github.com/resend/resend-go/v2"
)

// ResendSender is the default production driver.
type ResendSender struct {
	client    *resend.Client
	fromName  string
	fromEmail string
	logger    *slog.Logger
}

func NewResendSender(apiKey, fromName, fromEmail string, logger *slog.Logger) *ResendSender {
	return &ResendSender{
		client:    resend.NewClient(apiKey),
		fromName:  fromName,
		fromEmail: fromEmail,
		logger:    logger,
	}
}

// Send delivers msg through Resend. Errors surface verbatim so handlers can
// decide whether to retry, ignore (magic-link-already-exists case), or return
// 5xx to the client.
func (r *ResendSender) Send(ctx context.Context, msg Message) error {
	if len(msg.To) == 0 {
		return errors.New("email: no recipients")
	}
	if msg.Subject == "" {
		return errors.New("email: subject required")
	}

	from := r.fromEmail
	if r.fromName != "" {
		from = fmt.Sprintf("%s <%s>", r.fromName, r.fromEmail)
	}

	params := &resend.SendEmailRequest{
		From:    from,
		To:      msg.To,
		Subject: msg.Subject,
		Html:    msg.HTMLBody,
		Text:    msg.TextBody,
	}
	for k, v := range msg.Tags {
		params.Tags = append(params.Tags, resend.Tag{Name: k, Value: v})
	}

	sent, err := r.client.Emails.SendWithContext(ctx, params)
	if err != nil {
		return fmt.Errorf("resend send: %w", err)
	}
	r.logger.LogAttrs(ctx, slog.LevelInfo, "email sent",
		slog.String("resend_id", sent.Id),
		slog.String("subject", msg.Subject),
		slog.Int("to_count", len(msg.To)),
	)
	return nil
}
