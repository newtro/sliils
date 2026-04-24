package workers

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/sliils/sliils/apps/server/internal/calsync"
	"github.com/sliils/sliils/apps/server/internal/db/sqlcgen"
)

// Calendar sync workers (M9-P3).
//
// Two job shapes:
//
//   CalendarPushJob    per-event job enqueued when a user's event is
//                      created/updated/cancelled. Iterates the event
//                      creator's external_calendars and writes the
//                      change to each provider.
//
//   CalendarPullJob    periodic (60s). For every active connection,
//                      pulls the provider's delta since last sync token
//                      and upserts into events, tagged with external_*.
//
// We intentionally keep push + pull in separate jobs so failures in one
// direction don't stall the other, and the periodic pull can run even
// when no outbound changes are happening.

// ---- CalendarPushJob ---------------------------------------------------

type CalendarPushArgs struct {
	EventID    int64  `json:"event_id"`
	UserID     int64  `json:"user_id"`     // the event creator
	Action     string `json:"action"`      // "upsert" | "delete"
}

func (CalendarPushArgs) Kind() string { return "calendar.push" }

type CalendarPushWorker struct {
	river.WorkerDefaults[CalendarPushArgs]
	pool    *pgxpool.Pool
	calSync *calsync.Service
	logger  *slog.Logger
}

func (w *CalendarPushWorker) Work(ctx context.Context, job *river.Job[CalendarPushArgs]) error {
	q := sqlcgen.New(w.pool)

	// Gather active external calendar connections for the creator.
	conns, err := q.ListExternalCalendarsForUser(ctx, job.Args.UserID)
	if err != nil {
		w.logger.Warn("push: list connections failed", slog.String("error", err.Error()))
		return nil
	}
	if len(conns) == 0 {
		return nil
	}

	// Load the event. May be already canceled (action="delete").
	ev, err := q.GetEventByID(ctx, job.Args.EventID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}

	// Skip events that originated from an external provider. Otherwise
	// we'd push an imported Google event back to Google, producing an
	// infinite echo. A more sophisticated sync would track per-provider
	// origin; for v1 we just skip cross-provider re-push.
	if ev.ExternalProvider != nil {
		return nil
	}

	attendees, err := q.ListAttendeesForEvent(ctx, ev.ID)
	if err != nil {
		attendees = nil
	}

	nativeEvent := nativeEventFromRow(&ev, attendees)

	for _, conn := range conns {
		provider, ok := w.calSync.Provider(conn.Provider)
		if !ok {
			continue
		}
		refresh, err := w.calSync.Decrypt(conn.OauthRefreshToken)
		if err != nil {
			w.logger.Warn("push: decrypt refresh token failed",
				slog.Int64("user_id", conn.UserID),
				slog.String("provider", conn.Provider),
				slog.String("error", err.Error()))
			continue
		}

		pushCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		switch job.Args.Action {
		case "delete":
			// We don't currently track per-connection external IDs for
			// SliilS-native events. Deletes on the external side depend
			// on an existing external_event_id being populated. Future:
			// track (event_id, provider, external_id) in a side table.
			if ev.ExternalEventID != nil {
				if err := provider.Delete(pushCtx, string(refresh), *ev.ExternalEventID); err != nil && !calsync.IsUnimplemented(err) {
					w.logger.Warn("push: delete failed", slog.String("error", err.Error()))
				}
			}
		case "upsert":
			existingID := ""
			if ev.ExternalProvider != nil && *ev.ExternalProvider == conn.Provider && ev.ExternalEventID != nil {
				existingID = *ev.ExternalEventID
			}
			newID, etag, err := provider.Push(pushCtx, string(refresh), nativeEvent, existingID)
			if err != nil {
				if calsync.IsUnimplemented(err) {
					cancel()
					continue
				}
				w.logger.Warn("push: upsert failed", slog.String("error", err.Error()))
				cancel()
				continue
			}
			// Record the external id on our row so subsequent updates
			// target the existing provider-side event.
			provName := conn.Provider
			if err := q.SetEventExternalRef(ctx, sqlcgen.SetEventExternalRefParams{
				ID:               ev.ID,
				ExternalProvider: &provName,
				ExternalEventID:  &newID,
				ExternalEtag:     &etag,
			}); err != nil {
				w.logger.Warn("push: stamp external id failed", slog.String("error", err.Error()))
			}
		}
		cancel()
	}
	return nil
}

// ---- CalendarPullJob ---------------------------------------------------

type CalendarPullArgs struct{}

func (CalendarPullArgs) Kind() string { return "calendar.pull" }

type CalendarPullWorker struct {
	river.WorkerDefaults[CalendarPullArgs]
	pool    *pgxpool.Pool
	calSync *calsync.Service
	logger  *slog.Logger
}

func (w *CalendarPullWorker) Work(ctx context.Context, job *river.Job[CalendarPullArgs]) error {
	q := sqlcgen.New(w.pool)

	conns, err := q.ListActiveExternalCalendars(ctx)
	if err != nil {
		return err
	}
	if len(conns) == 0 {
		return nil
	}

	for _, conn := range conns {
		provider, ok := w.calSync.Provider(conn.Provider)
		if !ok {
			continue
		}
		refresh, err := w.calSync.Decrypt(conn.OauthRefreshToken)
		if err != nil {
			w.logger.Warn("pull: decrypt refresh token failed",
				slog.Int64("user_id", conn.UserID),
				slog.String("provider", conn.Provider),
				slog.String("error", err.Error()))
			continue
		}

		syncToken := ""
		if conn.SyncToken != nil {
			syncToken = *conn.SyncToken
		}

		pullCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		res, err := provider.Pull(pullCtx, string(refresh), syncToken)
		cancel()
		if err != nil {
			if calsync.IsUnimplemented(err) {
				continue
			}
			if errors.Is(err, calsync.ErrNeedsReauth) {
				w.logger.Warn("pull: reauth required — marking disconnected",
					slog.Int64("user_id", conn.UserID),
					slog.String("provider", conn.Provider))
				_ = q.DisconnectExternalCalendar(ctx, sqlcgen.DisconnectExternalCalendarParams{
					UserID:   conn.UserID,
					Provider: conn.Provider,
				})
				continue
			}
			w.logger.Warn("pull: provider error",
				slog.String("provider", conn.Provider),
				slog.String("error", err.Error()))
			continue
		}
		// Apply changes.
		w.applyPullChanges(ctx, q, conn.UserID, conn.Provider, res.Changed)

		// Stamp new cursor.
		nextToken := res.IncrementalCursor
		var tokenPtr *string
		if nextToken != "" {
			tokenPtr = &nextToken
		}
		if err := q.UpdateExternalCalendarSyncState(ctx, sqlcgen.UpdateExternalCalendarSyncStateParams{
			UserID:    conn.UserID,
			Provider:  conn.Provider,
			SyncToken: tokenPtr,
			Column4:   []byte(`{}`),
		}); err != nil {
			w.logger.Warn("pull: update sync state failed", slog.String("error", err.Error()))
		}
	}
	return nil
}

// applyPullChanges upserts each changed event into SliilS, tagged with
// external_provider + external_event_id so the push path can recognize
// them and skip. NOTE: we apply upserts via raw SQL under owner pool —
// events_tenant RLS would gate us on app.workspace_id which we don't
// know for an imported-event yet. v1: store every imported event in the
// user's FIRST (or only) workspace. Multi-workspace routing for
// imported events is a v1.1 story.
func (w *CalendarPullWorker) applyPullChanges(
	ctx context.Context,
	q *sqlcgen.Queries,
	userID int64,
	provider string,
	changes []calsync.ChangedEvent,
) {
	// Look up the user's default workspace (first one they created).
	workspaces, err := q.ListWorkspacesForUser(ctx, userID)
	if err != nil || len(workspaces) == 0 {
		return
	}
	defaultWS := workspaces[0].ID

	for _, ch := range changes {
		if ch.Deleted {
			// Mark the SliilS event as cancelled if we have it.
			existing, err := q.GetEventByExternalID(ctx, sqlcgen.GetEventByExternalIDParams{
				ExternalProvider: &provider,
				ExternalEventID:  &ch.ExternalID,
			})
			if err != nil {
				continue
			}
			_ = q.CancelEvent(ctx, existing.ID)
			continue
		}
		if ch.Event == nil {
			continue
		}

		existing, err := q.GetEventByExternalID(ctx, sqlcgen.GetEventByExternalIDParams{
			ExternalProvider: &provider,
			ExternalEventID:  &ch.ExternalID,
		})
		if err == nil {
			// Update.
			title := ch.Event.Title
			desc := ch.Event.Description
			loc := ch.Event.Location
			start := pgtype.Timestamptz{Time: ch.Event.Start, Valid: true}
			end := pgtype.Timestamptz{Time: ch.Event.End, Valid: true}
			tz := ch.Event.TimeZone
			var rrulePtr *string
			if ch.Event.RRule != "" {
				r := ch.Event.RRule
				rrulePtr = &r
			}
			if _, err := q.UpdateEvent(ctx, sqlcgen.UpdateEventParams{
				ID:          existing.ID,
				Title:       &title,
				Description: &desc,
				LocationUrl: &loc,
				StartAt:     start,
				EndAt:       end,
				TimeZone:    &tz,
				Rrule:       rrulePtr,
			}); err != nil {
				w.logger.Warn("pull: update event failed", slog.String("error", err.Error()))
			}
			etag := ch.ETag
			_ = q.SetEventExternalRef(ctx, sqlcgen.SetEventExternalRefParams{
				ID:               existing.ID,
				ExternalProvider: &provider,
				ExternalEventID:  &ch.ExternalID,
				ExternalEtag:     &etag,
			})
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			continue
		}

		// New import.
		var rrulePtr *string
		if ch.Event.RRule != "" {
			r := ch.Event.RRule
			rrulePtr = &r
		}
		prov := provider
		extID := ch.ExternalID
		etag := ch.ETag
		_, err = q.CreateEvent(ctx, sqlcgen.CreateEventParams{
			WorkspaceID:      defaultWS,
			Title:            ch.Event.Title,
			Description:      ch.Event.Description,
			LocationUrl:      ch.Event.Location,
			StartAt:          pgtype.Timestamptz{Time: ch.Event.Start, Valid: true},
			EndAt:            pgtype.Timestamptz{Time: ch.Event.End, Valid: true},
			TimeZone:         ch.Event.TimeZone,
			Rrule:            rrulePtr,
			RecordingEnabled: false,
			VideoEnabled:     false,
			CreatedBy:        &userID,
			ExternalProvider: &prov,
			ExternalEventID:  &extID,
			ExternalEtag:     &etag,
		})
		if err != nil {
			w.logger.Warn("pull: create imported event failed", slog.String("error", err.Error()))
		}
	}
}

// ---- helpers -----------------------------------------------------------

func nativeEventFromRow(ev *sqlcgen.Event, atts []sqlcgen.ListAttendeesForEventRow) *calsync.Event {
	out := &calsync.Event{
		Title:       ev.Title,
		Description: ev.Description,
		Location:    ev.LocationUrl,
		Start:       ev.StartAt.Time,
		End:         ev.EndAt.Time,
		TimeZone:    ev.TimeZone,
	}
	if ev.Rrule != nil {
		out.RRule = *ev.Rrule
	}
	for _, a := range atts {
		fa := calsync.Attendee{RSVP: a.Rsvp}
		if a.ExternalEmail != nil {
			fa.Email = *a.ExternalEmail
		} else if a.UserEmail != nil {
			fa.Email = *a.UserEmail
			if a.DisplayName != nil {
				fa.DisplayName = *a.DisplayName
			}
		}
		if fa.Email != "" {
			out.Attendees = append(out.Attendees, fa)
		}
	}
	return out
}
