// Package email provides the transactional email interface and its drivers.
//
// Sender is the seam between the app and whichever delivery backend is
// configured. M1 ships a single driver (Resend). Additional drivers (SMTP,
// Postmark, AWS SES) slot in without touching call sites.
package email

import "context"

// Message is the transport-agnostic email payload.
type Message struct {
	To       []string
	Subject  string
	HTMLBody string
	TextBody string
	// Tags are string key/value pairs used by drivers that support analytics
	// segmentation (e.g. Resend's `tags`). Drivers that don't support tags
	// ignore the field.
	Tags map[string]string
}

// Sender delivers transactional emails. Implementations must be safe for
// concurrent use — handlers call Send directly without further locking.
type Sender interface {
	Send(ctx context.Context, msg Message) error
}

// NoopSender drops every message on the floor. Useful for tests and for
// dev runs where email delivery is intentionally disabled.
type NoopSender struct {
	// Sent, when non-nil, receives every Send call so tests can assert on
	// outgoing mail without hitting a real provider.
	Sent chan<- Message
}

func (n NoopSender) Send(_ context.Context, msg Message) error {
	if n.Sent != nil {
		select {
		case n.Sent <- msg:
		default:
		}
	}
	return nil
}
