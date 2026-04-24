package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"

	"github.com/sliils/sliils/apps/server/internal/calendar"
	"github.com/sliils/sliils/apps/server/internal/calls"
	"github.com/sliils/sliils/apps/server/internal/db"
	"github.com/sliils/sliils/apps/server/internal/db/sqlcgen"
	"github.com/sliils/sliils/apps/server/internal/problem"
)

// Calendar events (M9).
//
// Surface:
//   GET    /workspaces/:slug/events?from=...&to=...
//   POST   /workspaces/:slug/events
//   PATCH  /events/:id
//   DELETE /events/:id
//   POST   /events/:id/rsvp
//   POST   /events/:id/join          — bridges to M8 meetings
//
// RRULE expansion happens in Go; the DB stores the canonical series row.
// Each occurrence in the GET response includes a synthetic id so clients
// can keep stable keys (event_id + instance_start).

// ---- DTOs ---------------------------------------------------------------

type EventDTO struct {
	ID               int64      `json:"id"`
	WorkspaceID      int64      `json:"workspace_id"`
	ChannelID        *int64     `json:"channel_id,omitempty"`
	Title            string     `json:"title"`
	Description      string     `json:"description"`
	LocationURL      string     `json:"location_url,omitempty"`
	StartAt          time.Time  `json:"start_at"`
	EndAt            time.Time  `json:"end_at"`
	TimeZone         string     `json:"time_zone"`
	RRule            string     `json:"rrule,omitempty"`
	RecordingEnabled bool       `json:"recording_enabled"`
	VideoEnabled     bool       `json:"video_enabled"`
	CreatedBy        *int64     `json:"created_by,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	CanceledAt       *time.Time `json:"canceled_at,omitempty"`
	Attendees        []AttendeeDTO `json:"attendees,omitempty"`
	MyRSVP           string     `json:"my_rsvp,omitempty"`
}

type AttendeeDTO struct {
	UserID        *int64 `json:"user_id,omitempty"`
	DisplayName   string `json:"display_name,omitempty"`
	Email         string `json:"email,omitempty"`
	ExternalEmail string `json:"external_email,omitempty"`
	RSVP          string `json:"rsvp"`
}

// OccurrenceDTO is what GET /events returns — one per RRULE-expanded
// instance. SeriesID points back to the events.id row so edits go to
// the right series.
type OccurrenceDTO struct {
	SeriesID         int64         `json:"series_id"`
	InstanceStart    time.Time     `json:"instance_start"`
	InstanceEnd      time.Time     `json:"instance_end"`
	Title            string        `json:"title"`
	Description      string        `json:"description"`
	LocationURL      string        `json:"location_url,omitempty"`
	ChannelID        *int64        `json:"channel_id,omitempty"`
	TimeZone         string        `json:"time_zone"`
	RRule            string        `json:"rrule,omitempty"`
	RecordingEnabled bool          `json:"recording_enabled"`
	VideoEnabled     bool          `json:"video_enabled"`
	CreatedBy        *int64        `json:"created_by,omitempty"`
	MyRSVP           string        `json:"my_rsvp,omitempty"`
	Attendees        []AttendeeDTO `json:"attendees,omitempty"`
}

type createEventRequest struct {
	ChannelID        *int64   `json:"channel_id,omitempty"`
	Title            string   `json:"title"`
	Description      string   `json:"description,omitempty"`
	LocationURL      string   `json:"location_url,omitempty"`
	StartAt          time.Time `json:"start_at"`
	EndAt            time.Time `json:"end_at"`
	TimeZone         string   `json:"time_zone,omitempty"`
	RRule            string   `json:"rrule,omitempty"`
	RecordingEnabled bool     `json:"recording_enabled,omitempty"`
	VideoEnabled     *bool    `json:"video_enabled,omitempty"`
	AttendeeUserIDs  []int64  `json:"attendee_user_ids,omitempty"`
	ExternalEmails   []string `json:"external_emails,omitempty"`
}

type patchEventRequest struct {
	Title            *string   `json:"title,omitempty"`
	Description      *string   `json:"description,omitempty"`
	LocationURL      *string   `json:"location_url,omitempty"`
	StartAt          *time.Time `json:"start_at,omitempty"`
	EndAt            *time.Time `json:"end_at,omitempty"`
	TimeZone         *string   `json:"time_zone,omitempty"`
	RRule            *string   `json:"rrule,omitempty"`
	RecordingEnabled *bool     `json:"recording_enabled,omitempty"`
	VideoEnabled     *bool     `json:"video_enabled,omitempty"`
}

type rsvpRequest struct {
	RSVP string `json:"rsvp"`
}

// ---- routes -------------------------------------------------------------

func (s *Server) mountEvents(api *echo.Group) {
	g := api.Group("")
	g.Use(s.requireAuth())
	g.Use(s.requireTenantWriteLimit())

	g.GET("/workspaces/:slug/events", s.listEventsRange)
	g.POST("/workspaces/:slug/events", s.createEvent)
	g.PATCH("/events/:id", s.patchEvent)
	g.DELETE("/events/:id", s.cancelEvent)
	g.POST("/events/:id/rsvp", s.rsvpEvent)
	g.POST("/events/:id/join", s.joinEventMeeting)
}

// ---- list with expansion ------------------------------------------------

func (s *Server) listEventsRange(c echo.Context) error {
	user := userFromContext(c)
	slug := c.Param("slug")

	ws, err := s.resolveWorkspaceBySlug(c.Request().Context(), user.ID, slug)
	if err != nil {
		return err
	}

	// from / to default to "the next four weeks" so the calendar UI can
	// paint a landing view with no params.
	fromStr := c.QueryParam("from")
	toStr := c.QueryParam("to")
	var from, to time.Time
	now := time.Now()
	if fromStr == "" {
		from = now.Add(-7 * 24 * time.Hour)
	} else {
		from, err = time.Parse(time.RFC3339, fromStr)
		if err != nil {
			return problem.BadRequest("from must be RFC3339: " + err.Error())
		}
	}
	if toStr == "" {
		to = now.Add(28 * 24 * time.Hour)
	} else {
		to, err = time.Parse(time.RFC3339, toStr)
		if err != nil {
			return problem.BadRequest("to must be RFC3339: " + err.Error())
		}
	}
	if !to.After(from) {
		return problem.BadRequest("to must be after from")
	}

	var rows []sqlcgen.ListEventsInRangeRow
	var attendeeRows []sqlcgen.ListAttendeesForEventRow
	err = db.WithTx(c.Request().Context(), s.pool.Pool,
		db.TxOptions{UserID: user.ID, WorkspaceID: ws.ID, ReadOnly: true},
		func(scope db.TxScope) error {
			r, err := scope.Queries.ListEventsInRange(c.Request().Context(), sqlcgen.ListEventsInRangeParams{
				WorkspaceID: ws.ID,
				EndAt:       pgtype.Timestamptz{Time: from, Valid: true},
				StartAt:     pgtype.Timestamptz{Time: to, Valid: true},
			})
			if err != nil {
				return err
			}
			rows = r

			// One batch attendee fetch per range response. For a typical
			// calendar view (~50 events), this is a single round-trip.
			for _, row := range r {
				att, err := scope.Queries.ListAttendeesForEvent(c.Request().Context(), row.ID)
				if err != nil {
					return err
				}
				attendeeRows = append(attendeeRows, att...)
			}
			return nil
		})
	if err != nil {
		return problem.Internal("list events: " + err.Error())
	}

	// Index attendees by event id.
	attendeeByEvent := make(map[int64][]AttendeeDTO, len(rows))
	for _, a := range attendeeRows {
		dto := AttendeeDTO{RSVP: a.Rsvp}
		if a.UserID != nil {
			dto.UserID = a.UserID
			dto.DisplayName = orEmpty(a.DisplayName)
			dto.Email = orEmpty(a.UserEmail)
		}
		if a.ExternalEmail != nil {
			dto.ExternalEmail = *a.ExternalEmail
		}
		attendeeByEvent[a.EventID] = append(attendeeByEvent[a.EventID], dto)
	}

	var out []OccurrenceDTO
	for _, row := range rows {
		rrule := ""
		if row.Rrule != nil {
			rrule = *row.Rrule
		}
		occurrences, err := calendar.Expand(row.ID, row.StartAt.Time, row.EndAt.Time, rrule, row.TimeZone, from, to)
		if err != nil {
			s.logger.Warn("rrule expand failed; skipping series",
				"event_id", row.ID, "error", err.Error())
			continue
		}
		myRSVP := attendeeRSVPFor(attendeeByEvent[row.ID], user.ID)
		for _, occ := range occurrences {
			out = append(out, OccurrenceDTO{
				SeriesID:         row.ID,
				InstanceStart:    occ.InstanceStart,
				InstanceEnd:      occ.InstanceEnd,
				Title:            row.Title,
				Description:      row.Description,
				LocationURL:      row.LocationUrl,
				ChannelID:        row.ChannelID,
				TimeZone:         row.TimeZone,
				RRule:            rrule,
				RecordingEnabled: row.RecordingEnabled,
				VideoEnabled:     row.VideoEnabled,
				CreatedBy:        row.CreatedBy,
				Attendees:        attendeeByEvent[row.ID],
				MyRSVP:           myRSVP,
			})
		}
	}

	// out is nil when the window has no events; normalize to [] so the
	// client's TanStack Query treats empty as a valid cached value.
	if out == nil {
		out = []OccurrenceDTO{}
	}
	return c.JSON(http.StatusOK, out)
}

// ---- create -------------------------------------------------------------

func (s *Server) createEvent(c echo.Context) error {
	user := userFromContext(c)
	slug := c.Param("slug")

	ws, err := s.resolveWorkspaceBySlug(c.Request().Context(), user.ID, slug)
	if err != nil {
		return err
	}

	var req createEventRequest
	if err := c.Bind(&req); err != nil {
		return problem.BadRequest("invalid body")
	}
	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" {
		return problem.BadRequest("title is required")
	}
	if req.StartAt.IsZero() || req.EndAt.IsZero() {
		return problem.BadRequest("start_at and end_at are required (RFC3339)")
	}
	if !req.EndAt.After(req.StartAt) {
		return problem.BadRequest("end_at must be after start_at")
	}
	if req.TimeZone == "" {
		req.TimeZone = "UTC"
	}
	videoEnabled := true
	if req.VideoEnabled != nil {
		videoEnabled = *req.VideoEnabled
	}

	// Sanity-check RRULE before accepting it so a malformed rule can't
	// wedge the list-events expander later.
	if req.RRule != "" {
		if _, err := calendar.Expand(0, req.StartAt, req.EndAt, req.RRule, req.TimeZone, req.StartAt, req.StartAt.Add(365*24*time.Hour)); err != nil {
			return problem.BadRequest("invalid rrule: " + err.Error())
		}
	}

	var created sqlcgen.Event
	var attendeeDTOs []AttendeeDTO
	err = db.WithTx(c.Request().Context(), s.pool.Pool,
		db.TxOptions{UserID: user.ID, WorkspaceID: ws.ID},
		func(scope db.TxScope) error {
			var rrulePtr *string
			if req.RRule != "" {
				r := req.RRule
				rrulePtr = &r
			}
			e, err := scope.Queries.CreateEvent(c.Request().Context(), sqlcgen.CreateEventParams{
				WorkspaceID:      ws.ID,
				ChannelID:        req.ChannelID,
				Title:            req.Title,
				Description:      req.Description,
				LocationUrl:      req.LocationURL,
				StartAt:          pgtype.Timestamptz{Time: req.StartAt, Valid: true},
				EndAt:            pgtype.Timestamptz{Time: req.EndAt, Valid: true},
				TimeZone:         req.TimeZone,
				Rrule:            rrulePtr,
				RecordingEnabled: req.RecordingEnabled,
				VideoEnabled:     videoEnabled,
				CreatedBy:        &user.ID,
				ExternalProvider: nil,
				ExternalEventID:  nil,
				ExternalEtag:     nil,
			})
			if err != nil {
				return err
			}
			created = e

			// The creator is implicitly invited + RSVP=yes. Anyone can
			// decline later; making the default explicit avoids a "host
			// hasn't responded" indicator on their own event.
			pendingYes := "yes"
			if _, err := scope.Queries.UpsertInternalAttendee(c.Request().Context(), sqlcgen.UpsertInternalAttendeeParams{
				EventID: e.ID,
				UserID:  &user.ID,
				Column3: &pendingYes,
			}); err != nil {
				return err
			}
			attendeeDTOs = append(attendeeDTOs, AttendeeDTO{
				UserID:      &user.ID,
				DisplayName: user.DisplayName,
				Email:       user.Email,
				RSVP:        "yes",
			})

			for _, uid := range req.AttendeeUserIDs {
				if uid == user.ID {
					continue
				}
				att, err := scope.Queries.UpsertInternalAttendee(c.Request().Context(), sqlcgen.UpsertInternalAttendeeParams{
					EventID: e.ID,
					UserID:  &uid,
					Column3: nil,
				})
				if err != nil {
					return err
				}
				attendeeDTOs = append(attendeeDTOs, AttendeeDTO{
					UserID: att.UserID,
					RSVP:   att.Rsvp,
				})
			}
			for _, email := range req.ExternalEmails {
				email = strings.TrimSpace(email)
				if email == "" {
					continue
				}
				att, err := scope.Queries.UpsertExternalAttendee(c.Request().Context(), sqlcgen.UpsertExternalAttendeeParams{
					EventID:       e.ID,
					ExternalEmail: &email,
					Column3:       nil,
				})
				if err != nil {
					return err
				}
				attendeeDTOs = append(attendeeDTOs, AttendeeDTO{
					ExternalEmail: orEmpty(att.ExternalEmail),
					RSVP:          att.Rsvp,
				})
			}
			return nil
		})
	if err != nil {
		return problem.Internal("create event: " + err.Error())
	}

	s.enqueueCalendarPush(c.Request().Context(), created.ID, user.ID, "upsert")

	dto := eventDTOFromRow(&created)
	dto.Attendees = attendeeDTOs
	dto.MyRSVP = "yes"
	return c.JSON(http.StatusCreated, dto)
}

// ---- patch / cancel -----------------------------------------------------

func (s *Server) patchEvent(c echo.Context) error {
	user := userFromContext(c)
	id, err := parsePathInt64(c, "id")
	if err != nil {
		return problem.BadRequest("invalid id")
	}

	ev, workspaceID, err := s.fetchEventForUser(c.Request().Context(), user.ID, id)
	if err != nil {
		return err
	}
	if !canEditEvent(user.ID, ev) {
		return problem.Forbidden("only the event creator can edit")
	}

	var req patchEventRequest
	if err := c.Bind(&req); err != nil {
		return problem.BadRequest("invalid body")
	}

	params := sqlcgen.UpdateEventParams{ID: id}
	if req.Title != nil {
		params.Title = req.Title
	}
	if req.Description != nil {
		params.Description = req.Description
	}
	if req.LocationURL != nil {
		params.LocationUrl = req.LocationURL
	}
	if req.StartAt != nil {
		params.StartAt = pgtype.Timestamptz{Time: *req.StartAt, Valid: true}
	}
	if req.EndAt != nil {
		params.EndAt = pgtype.Timestamptz{Time: *req.EndAt, Valid: true}
	}
	if req.TimeZone != nil {
		params.TimeZone = req.TimeZone
	}
	if req.RRule != nil {
		if *req.RRule != "" {
			if _, err := calendar.Expand(0, ev.StartAt.Time, ev.EndAt.Time, *req.RRule, ev.TimeZone, ev.StartAt.Time, ev.StartAt.Time.Add(365*24*time.Hour)); err != nil {
				return problem.BadRequest("invalid rrule: " + err.Error())
			}
		}
		params.Rrule = req.RRule
	}
	if req.RecordingEnabled != nil {
		params.RecordingEnabled = req.RecordingEnabled
	}
	if req.VideoEnabled != nil {
		params.VideoEnabled = req.VideoEnabled
	}

	var updated sqlcgen.Event
	err = db.WithTx(c.Request().Context(), s.pool.Pool, db.TxOptions{UserID: user.ID, WorkspaceID: workspaceID}, func(scope db.TxScope) error {
		e, err := scope.Queries.UpdateEvent(c.Request().Context(), params)
		if err != nil {
			return err
		}
		updated = e
		return nil
	})
	if err != nil {
		return problem.Internal("update event: " + err.Error())
	}
	creatorID := user.ID
	if updated.CreatedBy != nil {
		creatorID = *updated.CreatedBy
	}
	s.enqueueCalendarPush(c.Request().Context(), updated.ID, creatorID, "upsert")
	return c.JSON(http.StatusOK, eventDTOFromRow(&updated))
}

func (s *Server) cancelEvent(c echo.Context) error {
	user := userFromContext(c)
	id, err := parsePathInt64(c, "id")
	if err != nil {
		return problem.BadRequest("invalid id")
	}
	ev, workspaceID, err := s.fetchEventForUser(c.Request().Context(), user.ID, id)
	if err != nil {
		return err
	}
	if !canEditEvent(user.ID, ev) {
		return problem.Forbidden("only the event creator can cancel")
	}

	err = db.WithTx(c.Request().Context(), s.pool.Pool, db.TxOptions{UserID: user.ID, WorkspaceID: workspaceID}, func(scope db.TxScope) error {
		return scope.Queries.CancelEvent(c.Request().Context(), id)
	})
	if err != nil {
		return problem.Internal("cancel event: " + err.Error())
	}
	creatorID := user.ID
	if ev.CreatedBy != nil {
		creatorID = *ev.CreatedBy
	}
	s.enqueueCalendarPush(c.Request().Context(), id, creatorID, "delete")
	return c.NoContent(http.StatusNoContent)
}

// enqueueCalendarPush queues a calendar-push job via the hook main.go
// wires after startup. Nil hook = external sync disabled → silent no-op.
func (s *Server) enqueueCalendarPush(ctx context.Context, eventID, userID int64, action string) {
	if s.enqueueCalPush == nil {
		return
	}
	if err := s.enqueueCalPush(ctx, eventID, userID, action); err != nil {
		s.logger.Warn("enqueue calendar push failed",
			"error", err.Error(),
			"event_id", eventID,
			"action", action,
		)
	}
}

// ---- RSVP --------------------------------------------------------------

func (s *Server) rsvpEvent(c echo.Context) error {
	user := userFromContext(c)
	id, err := parsePathInt64(c, "id")
	if err != nil {
		return problem.BadRequest("invalid id")
	}
	var req rsvpRequest
	if err := c.Bind(&req); err != nil {
		return problem.BadRequest("invalid body")
	}
	switch req.RSVP {
	case "yes", "no", "maybe", "pending":
	default:
		return problem.BadRequest("rsvp must be one of yes|no|maybe|pending")
	}

	_, workspaceID, err := s.fetchEventForUser(c.Request().Context(), user.ID, id)
	if err != nil {
		return err
	}

	err = db.WithTx(c.Request().Context(), s.pool.Pool, db.TxOptions{UserID: user.ID, WorkspaceID: workspaceID}, func(scope db.TxScope) error {
		// Ensure the user is on the invite list before recording. If
		// they weren't originally invited but they can see the event
		// (workspace member), they self-opt-in — this is a public-
		// channel-event semantics choice.
		if _, err := scope.Queries.UpsertInternalAttendee(c.Request().Context(), sqlcgen.UpsertInternalAttendeeParams{
			EventID: id,
			UserID:  &user.ID,
			Column3: nil,
		}); err != nil {
			return err
		}
		_, err := scope.Queries.UpdateAttendeeRSVP(c.Request().Context(), sqlcgen.UpdateAttendeeRSVPParams{
			EventID: id,
			UserID:  &user.ID,
			Rsvp:    req.RSVP,
		})
		return err
	})
	if err != nil {
		return problem.Internal("rsvp: " + err.Error())
	}
	return c.NoContent(http.StatusNoContent)
}

// ---- event → meeting bridge --------------------------------------------

// joinEventMeeting is the "Join" button on an event. If the event has
// video_enabled and the user is an attendee (or workspace member for
// channel-events), we find-or-create a meeting on the event's channel
// and return a join token. No channel? Return 409 — nowhere to host it.
func (s *Server) joinEventMeeting(c echo.Context) error {
	user := userFromContext(c)
	if s.calls == nil {
		return problem.ServiceUnavailable("calls are not enabled on this install")
	}
	id, err := parsePathInt64(c, "id")
	if err != nil {
		return problem.BadRequest("invalid id")
	}

	ev, _, err := s.fetchEventForUser(c.Request().Context(), user.ID, id)
	if err != nil {
		return err
	}
	if !ev.VideoEnabled {
		return problem.Conflict("this event has no video call")
	}
	if ev.ChannelID == nil {
		return problem.Conflict("this event is not attached to a channel; cannot host a call")
	}

	// Delegate to the same start-or-get flow used by channel call buttons.
	var meeting sqlcgen.Meeting
	err = db.WithTx(c.Request().Context(), s.pool.Pool,
		db.TxOptions{UserID: user.ID, WorkspaceID: ev.WorkspaceID},
		func(scope db.TxScope) error {
			existing, err := scope.Queries.GetActiveMeetingForChannel(c.Request().Context(), *ev.ChannelID)
			if err == nil {
				meeting = existing
				return nil
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			m, err := scope.Queries.CreateMeeting(c.Request().Context(), sqlcgen.CreateMeetingParams{
				WorkspaceID: ev.WorkspaceID,
				ChannelID:   *ev.ChannelID,
				LivekitRoom: fmt.Sprintf("pending-%d-%d", ev.WorkspaceID, time.Now().UnixNano()),
				StartedBy:   &user.ID,
				Column5:     []byte(`{}`),
			})
			if err != nil {
				return err
			}
			roomName := calls.RoomNameForMeeting(m.ID)
			if err := scope.Queries.SetMeetingLiveKitRoom(c.Request().Context(), sqlcgen.SetMeetingLiveKitRoomParams{
				ID:          m.ID,
				LivekitRoom: roomName,
			}); err != nil {
				return err
			}
			m.LivekitRoom = roomName
			meeting = m
			return nil
		})
	if err != nil {
		return problem.Internal("start meeting: " + err.Error())
	}

	displayName := user.DisplayName
	if displayName == "" {
		displayName = user.Email
	}
	token, err := s.calls.IssueJoin(calls.JoinClaims{
		RoomName:       meeting.LivekitRoom,
		ParticipantID:  fmt.Sprintf("%d", user.ID),
		DisplayName:    displayName,
		CanPublish:     true,
		CanSubscribe:   true,
		CanPublishData: true,
		TTL:            s.cfg.CallJoinTokenTTL,
	})
	if err != nil {
		return problem.Internal("issue join token: " + err.Error())
	}

	return c.JSON(http.StatusOK, JoinTokenDTO{
		Token:     token,
		WSURL:     s.calls.WSURL(),
		Room:      meeting.LivekitRoom,
		Identity:  fmt.Sprintf("%d", user.ID),
		ExpiresAt: time.Now().Add(s.cfg.CallJoinTokenTTL),
	})
}

// ---- helpers ----------------------------------------------------------

// fetchEventForUser locates an event by id under the user's visibility,
// trying each workspace they belong to. Mirrors the message-fetch helper
// we use elsewhere — one of the costs of cross-workspace ids.
func (s *Server) fetchEventForUser(ctx context.Context, userID, eventID int64) (*sqlcgen.Event, int64, error) {
	memberships, err := s.listUserWorkspaceIDs(ctx, userID)
	if err != nil {
		return nil, 0, problem.Internal("list workspaces: " + err.Error())
	}
	for _, wsID := range memberships {
		var ev sqlcgen.Event
		found := false
		err := db.WithTx(ctx, s.pool.Pool, db.TxOptions{UserID: userID, WorkspaceID: wsID, ReadOnly: true}, func(scope db.TxScope) error {
			e, err := scope.Queries.GetEventByID(ctx, eventID)
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return nil
				}
				return err
			}
			ev = e
			found = true
			return nil
		})
		if err != nil {
			return nil, 0, problem.Internal("fetch event: " + err.Error())
		}
		if found {
			return &ev, wsID, nil
		}
	}
	return nil, 0, problem.NotFound("event not found")
}

func canEditEvent(userID int64, ev *sqlcgen.Event) bool {
	return ev.CreatedBy != nil && *ev.CreatedBy == userID
}

func eventDTOFromRow(e *sqlcgen.Event) EventDTO {
	out := EventDTO{
		ID:               e.ID,
		WorkspaceID:      e.WorkspaceID,
		ChannelID:        e.ChannelID,
		Title:            e.Title,
		Description:      e.Description,
		LocationURL:      e.LocationUrl,
		StartAt:          e.StartAt.Time,
		EndAt:            e.EndAt.Time,
		TimeZone:         e.TimeZone,
		RecordingEnabled: e.RecordingEnabled,
		VideoEnabled:     e.VideoEnabled,
		CreatedBy:        e.CreatedBy,
		CreatedAt:        e.CreatedAt.Time,
		UpdatedAt:        e.UpdatedAt.Time,
	}
	if e.Rrule != nil {
		out.RRule = *e.Rrule
	}
	if e.CanceledAt.Valid {
		t := e.CanceledAt.Time
		out.CanceledAt = &t
	}
	return out
}

func attendeeRSVPFor(attendees []AttendeeDTO, userID int64) string {
	for _, a := range attendees {
		if a.UserID != nil && *a.UserID == userID {
			return a.RSVP
		}
	}
	return ""
}

func orEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
