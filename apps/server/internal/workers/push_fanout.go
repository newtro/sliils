package workers

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/sliils/sliils/apps/server/internal/db/sqlcgen"
	"github.com/sliils/sliils/apps/server/internal/push"
)

// Push fan-out worker (M11).
//
// Shape of a single job: "deliver a notification of type X for message
// Y to user Z". The worker resolves:
//   1. Do DND / quiet-hours / snooze suppress this recipient?
//   2. Is the channel muted or notify_pref="mute" for this user?
//   3. For each active device of the user, dispatch the opaque Payload.
//
// A single enqueue per (recipient, message_id, type). For a mention
// fanning out to 5 users, the messages handler enqueues 5 jobs.

type PushFanoutArgs struct {
	UserID    int64  `json:"user_id"`
	MsgID     string `json:"msg_id"`     // canonical message id (as a string so other notif types can reuse the slot)
	Type      string `json:"type"`       // "mention" | "dm" | "call" | "event"
	ChannelID string `json:"channel_id,omitempty"` // optional; drives per-channel mute check
	TenantURL string `json:"tenant_url,omitempty"`
}

func (PushFanoutArgs) Kind() string { return "push.fanout" }

type PushFanoutWorker struct {
	river.WorkerDefaults[PushFanoutArgs]
	pool    *pgxpool.Pool
	push    *push.Service
	logger  *slog.Logger
}

func (w *PushFanoutWorker) Work(ctx context.Context, job *river.Job[PushFanoutArgs]) error {
	q := sqlcgen.New(w.pool)

	// 1. DND gate. User-level suppression beats everything else.
	dnd, err := q.GetUserDNDState(ctx, job.Args.UserID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	state := push.StateFromRow(dnd.DndEnabledUntil, dnd.QuietHoursStart, dnd.QuietHoursEnd, dnd.QuietHoursTz)
	if state.ShouldSuppress(time.Now()) {
		w.logger.Debug("push suppressed by DND",
			slog.Int64("user_id", job.Args.UserID),
			slog.String("type", job.Args.Type))
		return nil
	}

	// 2. Per-channel mute. channel_memberships.muted_until wins; also a
	// notify_pref of 'mute' hard-mutes for everything but @-mentions.
	if job.Args.ChannelID != "" && job.Args.ChannelID != "0" {
		chID, err := parseInt64(job.Args.ChannelID)
		if err == nil {
			m, err := q.GetChannelMembership(ctx, sqlcgen.GetChannelMembershipParams{
				ChannelID: chID,
				UserID:    job.Args.UserID,
			})
			if err == nil {
				if m.MutedUntil.Valid && m.MutedUntil.Time.After(time.Now()) {
					w.logger.Debug("push suppressed by channel mute",
						slog.Int64("user_id", job.Args.UserID),
						slog.Int64("channel_id", chID))
					return nil
				}
				// notify_pref: 'all' | 'mentions' | 'mute'. We only
				// enqueue on mention/DM so 'mute' suppresses both.
				// 'mentions' would suppress non-mention DMs; our job
				// type makes that distinction obvious.
				if m.NotifyPref == "mute" {
					return nil
				}
				if m.NotifyPref == "mentions" && job.Args.Type != "mention" {
					return nil
				}
			}
		}
	}

	// 3. Hydrate devices and dispatch.
	devices, err := q.ListDevicesForPush(ctx, job.Args.UserID)
	if err != nil {
		return err
	}
	if len(devices) == 0 {
		return nil
	}
	payload := push.Payload{
		MsgID:     job.Args.MsgID,
		Type:      job.Args.Type,
		TenantURL: job.Args.TenantURL,
	}
	for _, d := range devices {
		target := push.Target{
			DeviceID:   d.ID,
			UserID:     d.UserID,
			Platform:   d.Platform,
			Endpoint:   d.Endpoint,
			P256DH:     d.P256dh,
			AuthSecret: d.AuthSecret,
		}
		devCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := w.push.Deliver(devCtx, target, payload)
		cancel()
		if err == nil {
			_ = q.TouchDevice(ctx, d.ID)
			continue
		}
		if errors.Is(err, push.ErrGone) {
			w.logger.Info("push endpoint gone; disabling device",
				slog.Int64("device_id", d.ID))
			_ = q.DisableDevice(ctx, sqlcgen.DisableDeviceParams{
				ID:             d.ID,
				DisabledReason: "endpoint gone (404/410)",
			})
			continue
		}
		w.logger.Warn("push deliver failed",
			slog.Int64("device_id", d.ID),
			slog.String("platform", d.Platform),
			slog.String("error", err.Error()))
	}
	return nil
}

// parseInt64 is a no-dep helper so the fan-out worker doesn't pull
// strconv. Returns -1 on failure — caller treats -1 as "no channel id".
func parseInt64(s string) (int64, error) {
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errBadInt
		}
		n = n*10 + int64(c-'0')
	}
	return n, nil
}

var errBadInt = errors.New("not a number")
