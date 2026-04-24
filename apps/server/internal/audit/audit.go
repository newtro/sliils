// Package audit records install- and workspace-level security events.
package audit

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/netip"

	"github.com/sliils/sliils/apps/server/internal/db/sqlcgen"
)

// Common actions. Keep the set short and use target_kind/target_id to add detail.
const (
	ActionSignup                = "auth.signup"
	ActionLoginSuccess          = "auth.login.success"
	ActionLoginFailure          = "auth.login.failure"
	ActionLoginLocked           = "auth.login.locked"
	ActionLogout                = "auth.logout"
	ActionPasswordResetRequest  = "auth.password_reset.request"
	ActionPasswordResetComplete = "auth.password_reset.complete"
	ActionMagicLinkRequest      = "auth.magic_link.request"
	ActionMagicLinkConsume      = "auth.magic_link.consume"
	ActionEmailVerifyRequest    = "auth.email_verify.request"
	ActionEmailVerifyComplete   = "auth.email_verify.complete"
	ActionTokenRefresh          = "auth.token.refresh"
)

// Event captures the fields callers supply; Recorder fills the rest.
type Event struct {
	WorkspaceID *int64
	ActorUserID *int64
	ActorIP     string // raw string form; parsed into inet by Recorder
	Action      string
	TargetKind  string
	TargetID    string
	Metadata    map[string]any
}

// Recorder is a thin wrapper over the sqlcgen queries that turns app-level
// events into audit_log rows. A nil Recorder is a no-op — handy for tests
// that don't care about audit assertions.
type Recorder struct {
	q      *sqlcgen.Queries
	logger *slog.Logger
}

func NewRecorder(q *sqlcgen.Queries, logger *slog.Logger) *Recorder {
	return &Recorder{q: q, logger: logger}
}

// Record persists one audit event. Errors are logged, not returned — audit
// write failures must never break the request path.
func (r *Recorder) Record(ctx context.Context, ev Event) {
	if r == nil || r.q == nil {
		return
	}

	var ip *netip.Addr
	if ev.ActorIP != "" {
		if addr, err := netip.ParseAddr(ev.ActorIP); err == nil {
			ip = &addr
		}
	}

	meta := []byte("{}")
	if len(ev.Metadata) > 0 {
		if b, err := json.Marshal(ev.Metadata); err == nil {
			meta = b
		}
	}

	var targetKind, targetID *string
	if ev.TargetKind != "" {
		targetKind = &ev.TargetKind
	}
	if ev.TargetID != "" {
		targetID = &ev.TargetID
	}

	if err := r.q.InsertAuditLog(ctx, sqlcgen.InsertAuditLogParams{
		WorkspaceID: ev.WorkspaceID,
		ActorUserID: ev.ActorUserID,
		ActorIp:     ip,
		Action:      ev.Action,
		TargetKind:  targetKind,
		TargetID:    targetID,
		Metadata:    meta,
	}); err != nil {
		r.logger.LogAttrs(ctx, slog.LevelWarn, "audit write failed",
			slog.String("action", ev.Action),
			slog.String("error", err.Error()),
		)
	}
}
