package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v4"

	"github.com/sliils/sliils/apps/server/internal/calls"
	"github.com/sliils/sliils/apps/server/internal/db"
	"github.com/sliils/sliils/apps/server/internal/db/sqlcgen"
	"github.com/sliils/sliils/apps/server/internal/problem"
	"github.com/sliils/sliils/apps/server/internal/realtime"
)

// Meeting endpoints (M8).
//
//   POST /channels/:channel_id/meetings            — start a new meeting
//                                                    (or return the active one)
//   POST /meetings/:id/join                        — mint a LiveKit join token
//   POST /meetings/:id/end                         — stamp ended_at, close the
//                                                    LiveKit room, post system msg
//   POST /meetings/:id/record/start  (stub/503)    — Egress deferred to M8.1
//   POST /meetings/:id/record/stop   (stub/503)
//
// Realtime events (broker topics ws:{workspace}:ch:{channel}):
//
//   meeting.started  {meeting_id, channel_id, started_by, livekit_room}
//   meeting.ended    {meeting_id, channel_id, ended_by, duration_seconds}

// ---- DTOs ---------------------------------------------------------------

type MeetingDTO struct {
	ID                int64     `json:"id"`
	ChannelID         int64     `json:"channel_id"`
	WorkspaceID       int64     `json:"workspace_id"`
	LiveKitRoom       string    `json:"livekit_room"`
	StartedBy         *int64    `json:"started_by,omitempty"`
	StartedAt         time.Time `json:"started_at"`
	EndedAt           *time.Time `json:"ended_at,omitempty"`
	ParticipantCount  int       `json:"participant_count"`
}

type JoinTokenDTO struct {
	Token     string `json:"token"`
	WSURL     string `json:"ws_url"`
	Room      string `json:"livekit_room"`
	Identity  string `json:"identity"`
	ExpiresAt time.Time `json:"expires_at"`
}

// ---- routes -------------------------------------------------------------

func (s *Server) mountMeetings(api *echo.Group) {
	if s.calls == nil {
		api.POST("/channels/:channel_id/meetings", s.callsDisabled)
		api.POST("/meetings/:id/join", s.callsDisabled)
		api.POST("/meetings/:id/end", s.callsDisabled)
		return
	}
	g := api.Group("")
	g.Use(s.requireAuth())
	g.POST("/channels/:channel_id/meetings", s.startOrGetMeeting)
	g.POST("/meetings/:id/join", s.joinMeeting)
	g.POST("/meetings/:id/end", s.endMeeting)
	// Recording stubs — behave like 501 while Egress is deferred. Wired
	// as handlers (rather than absent) so the frontend can display a
	// disabled button with a tooltip explaining why.
	g.POST("/meetings/:id/record/start", s.recordingDisabled)
	g.POST("/meetings/:id/record/stop", s.recordingDisabled)
}

func (s *Server) callsDisabled(c echo.Context) error {
	return problem.ServiceUnavailable("calls are not enabled on this install")
}

func (s *Server) recordingDisabled(c echo.Context) error {
	return problem.ServiceUnavailable("call recording is not yet available (requires LiveKit Egress + S3 storage from M5.1)")
}

// ---- startOrGetMeeting --------------------------------------------------

// startOrGetMeeting: if a meeting is already in progress on the channel,
// return it; otherwise create one. Makes "Call" button clicks idempotent
// across two users clicking at the same time.
func (s *Server) startOrGetMeeting(c echo.Context) error {
	user := userFromContext(c)
	if user == nil {
		return problem.Unauthorized("no user in context")
	}
	channelID, err := parsePathInt64(c, "channel_id")
	if err != nil {
		return problem.BadRequest("invalid channel_id")
	}

	workspaceID, err := s.resolveChannelWorkspace(c.Request().Context(), user.ID, channelID)
	if err != nil {
		return err
	}

	var meeting sqlcgen.Meeting
	created := false
	err = db.WithTx(c.Request().Context(), s.pool.Pool,
		db.TxOptions{UserID: user.ID, WorkspaceID: workspaceID},
		func(scope db.TxScope) error {
			existing, err := scope.Queries.GetActiveMeetingForChannel(c.Request().Context(), channelID)
			if err == nil {
				meeting = existing
				return nil
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			// Reserve a placeholder room name; we'll rewrite it to the
			// canonical "sliils-meeting-<id>" once we know the id. This
			// two-step dance is needed because livekit_room is UNIQUE
			// and computed from the id.
			m, err := scope.Queries.CreateMeeting(c.Request().Context(), sqlcgen.CreateMeetingParams{
				WorkspaceID: workspaceID,
				ChannelID:   channelID,
				LivekitRoom: fmt.Sprintf("pending-%d-%d", workspaceID, time.Now().UnixNano()),
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
			created = true
			return nil
		})
	if err != nil {
		return problem.Internal("start meeting: " + err.Error())
	}

	if created {
		s.publishMeetingEvent("meeting.started", workspaceID, channelID, map[string]any{
			"meeting_id":   meeting.ID,
			"channel_id":   channelID,
			"started_by":   user.ID,
			"livekit_room": meeting.LivekitRoom,
			"started_at":   meeting.StartedAt.Time.Format(time.RFC3339),
		})
	}
	return c.JSON(http.StatusOK, meetingDTO(&meeting))
}

// ---- joinMeeting -------------------------------------------------------

// joinMeeting issues the LiveKit JWT the browser uses to connect to the
// SFU. Only members of the channel the meeting belongs to can join.
func (s *Server) joinMeeting(c echo.Context) error {
	user := userFromContext(c)
	if user == nil {
		return problem.Unauthorized("no user in context")
	}
	meetingID, err := parsePathInt64(c, "id")
	if err != nil {
		return problem.BadRequest("invalid meeting id")
	}

	meeting, err := s.fetchMeetingForUser(c.Request().Context(), user.ID, meetingID)
	if err != nil {
		return err
	}
	if meeting.EndedAt.Valid {
		return problem.Conflict("meeting has ended")
	}

	displayName := user.DisplayName
	if displayName == "" {
		displayName = user.Email
	}

	token, err := s.calls.IssueJoin(calls.JoinClaims{
		RoomName:       meeting.LivekitRoom,
		ParticipantID:  strconv.FormatInt(user.ID, 10),
		DisplayName:    displayName,
		CanPublish:     true,
		CanSubscribe:   true,
		CanPublishData: true,
		CanUpdateRoom:  meeting.StartedBy != nil && *meeting.StartedBy == user.ID,
		TTL:            s.cfg.CallJoinTokenTTL,
	})
	if err != nil {
		return problem.Internal("issue join token: " + err.Error())
	}

	if err := s.incrementMeetingParticipants(c.Request().Context(), meeting.WorkspaceID, user.ID, meetingID); err != nil {
		s.logger.Warn("bump participant count failed", "error", err.Error(), "meeting_id", meetingID)
	}

	expires := time.Now().Add(s.cfg.CallJoinTokenTTL)
	return c.JSON(http.StatusOK, JoinTokenDTO{
		Token:     token,
		WSURL:     s.calls.WSURL(),
		Room:      meeting.LivekitRoom,
		Identity:  strconv.FormatInt(user.ID, 10),
		ExpiresAt: expires,
	})
}

// ---- endMeeting --------------------------------------------------------

// endMeeting stamps ended_at on the DB row, closes the LiveKit room, and
// posts a "Call ended, 7m12s" system message into the channel so the
// meeting leaves an artifact in the conversation history.
func (s *Server) endMeeting(c echo.Context) error {
	user := userFromContext(c)
	if user == nil {
		return problem.Unauthorized("no user in context")
	}
	meetingID, err := parsePathInt64(c, "id")
	if err != nil {
		return problem.BadRequest("invalid meeting id")
	}

	meeting, err := s.fetchMeetingForUser(c.Request().Context(), user.ID, meetingID)
	if err != nil {
		return err
	}
	if meeting.EndedAt.Valid {
		// Already ended — return the current row so the client doesn't trip.
		return c.JSON(http.StatusOK, meetingDTO(meeting))
	}

	var ended sqlcgen.Meeting
	err = db.WithTx(c.Request().Context(), s.pool.Pool,
		db.TxOptions{UserID: user.ID, WorkspaceID: meeting.WorkspaceID},
		func(scope db.TxScope) error {
			m, err := scope.Queries.EndMeeting(c.Request().Context(), sqlcgen.EndMeetingParams{
				ID:      meetingID,
				EndedBy: &user.ID,
			})
			if err != nil {
				return err
			}
			ended = m
			return nil
		})
	if err != nil {
		return problem.Internal("end meeting: " + err.Error())
	}

	// Close the LiveKit room so lingering clients get disconnected. Best-
	// effort — we don't fail the request on LiveKit errors because the DB
	// row is the source of truth.
	go func(room string) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.calls.EndRoom(ctx, room); err != nil {
			s.logger.Warn("close livekit room failed", "error", err.Error(), "room", room)
		}
	}(ended.LivekitRoom)

	// Post "Call ended, X" system message. Author is nil so the UI can
	// render it as a system message rather than a user message.
	duration := time.Duration(0)
	if ended.StartedAt.Valid && ended.EndedAt.Valid {
		duration = ended.EndedAt.Time.Sub(ended.StartedAt.Time)
	}
	body := fmt.Sprintf("📞 Call ended — %s", formatDuration(duration))
	if err := s.postSystemMessage(c.Request().Context(), ended.WorkspaceID, ended.ChannelID, body); err != nil {
		s.logger.Warn("post call-ended system message failed", "error", err.Error())
	}

	s.publishMeetingEvent("meeting.ended", ended.WorkspaceID, ended.ChannelID, map[string]any{
		"meeting_id":       ended.ID,
		"channel_id":       ended.ChannelID,
		"ended_by":         user.ID,
		"duration_seconds": int(duration.Seconds()),
	})

	return c.JSON(http.StatusOK, meetingDTO(&ended))
}

// ---- helpers -----------------------------------------------------------

// fetchMeetingForUser reads a meeting row under the user's RLS scope.
// Returns 404 if the meeting is in a workspace the user isn't a member
// of or has been deleted.
func (s *Server) fetchMeetingForUser(ctx context.Context, userID, meetingID int64) (*sqlcgen.Meeting, error) {
	memberships, err := s.listUserWorkspaceIDs(ctx, userID)
	if err != nil {
		return nil, problem.Internal("list workspaces: " + err.Error())
	}
	for _, wsID := range memberships {
		var out sqlcgen.Meeting
		found := false
		err := db.WithTx(ctx, s.pool.Pool, db.TxOptions{UserID: userID, WorkspaceID: wsID, ReadOnly: true}, func(scope db.TxScope) error {
			m, err := scope.Queries.GetMeetingByID(ctx, meetingID)
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return nil
				}
				return err
			}
			out = m
			found = true
			return nil
		})
		if err != nil {
			return nil, problem.Internal("fetch meeting: " + err.Error())
		}
		if found {
			return &out, nil
		}
	}
	return nil, problem.NotFound("meeting not found")
}

func (s *Server) incrementMeetingParticipants(ctx context.Context, workspaceID, userID, meetingID int64) error {
	return db.WithTx(ctx, s.pool.Pool, db.TxOptions{UserID: userID, WorkspaceID: workspaceID}, func(scope db.TxScope) error {
		return scope.Queries.BumpMeetingParticipantCount(ctx, meetingID)
	})
}

// postSystemMessage inserts a message with author_user_id = NULL + the
// given body into a channel. Skips the mention/attachment parsing that
// createMessage does — pure narrative text.
func (s *Server) postSystemMessage(ctx context.Context, workspaceID, channelID int64, body string) error {
	var m sqlcgen.Message
	err := db.WithTx(ctx, s.pool.Pool, db.TxOptions{WorkspaceID: workspaceID}, func(scope db.TxScope) error {
		row, err := scope.Queries.CreateMessage(ctx, sqlcgen.CreateMessageParams{
			WorkspaceID: workspaceID,
			ChannelID:   channelID,
			// AuthorUserID: nil — system message
			BodyMd:     body,
			BodyBlocks: []byte(`[]`),
		})
		if err != nil {
			return err
		}
		m = row
		return nil
	})
	if err != nil {
		return err
	}
	// Broadcast so connected clients see the "Call ended" line live.
	dto := messageFromRow(&m, nil, 0)
	dto.Attachments = []FileDTO{}
	s.publishMessage("message.created", workspaceID, channelID, dto)
	return nil
}

// publishMeetingEvent fans a call signal out on TWO topics:
//
//   channel-topic    — clients subscribed to the call's channel see it
//                      immediately (they already have that channel open).
//   workspace-topic  — clients anywhere in the workspace can listen for
//                      meeting.* events so an incoming-call overlay can
//                      ring even if the receiver isn't looking at the DM.
//
// The cost of a second publish is trivial (the broker is in-process) and
// it unlocks the global ringing UX without inventing per-user topics.
func (s *Server) publishMeetingEvent(eventType string, workspaceID, channelID int64, payload map[string]any) {
	b, err := json.Marshal(payload)
	if err != nil {
		s.logger.Warn("marshal meeting event failed", "error", err.Error(), "type", eventType)
		return
	}
	s.broker.Publish(realtime.TopicChannel(workspaceID, channelID), eventType, b)
	s.broker.Publish(realtime.TopicWorkspace(workspaceID), eventType, b)
}

func meetingDTO(m *sqlcgen.Meeting) MeetingDTO {
	out := MeetingDTO{
		ID:               m.ID,
		ChannelID:        m.ChannelID,
		WorkspaceID:      m.WorkspaceID,
		LiveKitRoom:      m.LivekitRoom,
		StartedAt:        m.StartedAt.Time,
		StartedBy:        m.StartedBy,
		ParticipantCount: int(m.ParticipantCount),
	}
	if m.EndedAt.Valid {
		t := m.EndedAt.Time
		out.EndedAt = &t
	}
	return out
}

// formatDuration produces "3m47s" / "1h02m" for the Call-ended message.
func formatDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh%02dm", h, m)
	}
	if m > 0 {
		return fmt.Sprintf("%dm%02ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}
