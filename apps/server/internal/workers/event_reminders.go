package workers

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/sliils/sliils/apps/server/internal/db/sqlcgen"
	"github.com/sliils/sliils/apps/server/internal/realtime"
)

// Event-reminder worker (M9-P2).
//
// Fires once a minute. For every event starting 4-6 minutes from now with
// a RSVP=yes attendee, publishes an `event.upcoming` realtime event on
// the attendee's workspace topic. The window (4-6 min) is wider than the
// 60-sec tick to tolerate jitter without missing a reminder.
//
// Dedupe: the same reminder can fire twice on adjacent ticks if the job
// takes more than 60 seconds. For v1 we lean on the client — toast
// de-dup happens in the web layer by (event_id, user_id) — rather than
// materializing a per-event sentinel table. Revisit if users report
// double-dings.

type EventReminderArgs struct{}

func (EventReminderArgs) Kind() string { return "calendar.event_reminder" }

type EventReminderWorker struct {
	river.WorkerDefaults[EventReminderArgs]
	pool   *pgxpool.Pool
	broker *realtime.Broker
	logger *slog.Logger
}

func (w *EventReminderWorker) Work(ctx context.Context, job *river.Job[EventReminderArgs]) error {
	q := sqlcgen.New(w.pool)
	rows, err := q.ListUpcomingEventsForReminders(ctx, sqlcgen.ListUpcomingEventsForRemindersParams{
		LeadMin: pgtype.Interval{Microseconds: int64(4 * time.Minute / time.Microsecond), Valid: true},
		LeadMax: pgtype.Interval{Microseconds: int64(6 * time.Minute / time.Microsecond), Valid: true},
	})
	if err != nil {
		w.logger.Warn("list upcoming events failed", slog.String("error", err.Error()))
		return nil
	}
	if len(rows) == 0 {
		return nil
	}

	for _, r := range rows {
		payload := map[string]any{
			"event_id":      r.EventID,
			"workspace_id":  r.WorkspaceID,
			"channel_id":    r.ChannelID,
			"title":         r.Title,
			"start_at":      r.StartAt.Time.UTC().Format(time.RFC3339),
			"video_enabled": r.VideoEnabled,
			"user_id":       r.UserID,
		}
		b, err := json.Marshal(payload)
		if err != nil {
			continue
		}
		// Publish on the workspace topic so wherever the attendee is, they
		// get the ping. The client filters on user_id == me.
		w.broker.Publish(realtime.TopicWorkspace(r.WorkspaceID), "event.upcoming", b)
	}
	w.logger.Debug("event reminders dispatched", slog.Int("count", len(rows)))
	return nil
}
