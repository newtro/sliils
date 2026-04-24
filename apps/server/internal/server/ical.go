package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"

	"github.com/sliils/sliils/apps/server/internal/calendar"
	"github.com/sliils/sliils/apps/server/internal/db"
	"github.com/sliils/sliils/apps/server/internal/db/sqlcgen"
	"github.com/sliils/sliils/apps/server/internal/problem"
)

// iCal feed export (M9-P4).
//
// Two endpoints:
//
//   GET /me/calendar.ics                                    — authenticated
//     Downloads a personal iCalendar of every workspace's events the user
//     can see, for the next 90 days. Serves as a one-shot export.
//
//   GET /feed/:user_id/:token/calendar.ics                   — token-auth
//     Subscription feed for calendar apps that add a URL once and poll
//     for updates. `token` is an HMAC over (user_id, signing-key) so the
//     URL can't be guessed but doesn't expire; users rotate by deleting
//     and regenerating via the Me endpoints (future).
//
// The feed path intentionally does NOT include workspace scope — a user
// usually wants one combined calendar URL. Per-workspace feeds can be
// added later if users ask.

const icalFeedHMACPurpose = "ical-feed-v1"

func (s *Server) mountICal(api *echo.Group) {
	authed := api.Group("")
	authed.Use(s.requireAuth())
	authed.GET("/me/calendar.ics", s.myICalExport)
	authed.GET("/me/calendar-feed", s.myICalFeedURL)

	// The feed endpoint is outside any requireAuth — token is the auth.
	api.GET("/feed/:user_id/:token/calendar.ics", s.icalFeed)
}

// myICalExport returns a one-shot download covering the next 90 days.
func (s *Server) myICalExport(c echo.Context) error {
	user := userFromContext(c)
	body, err := s.buildUserICal(c.Request().Context(), user.ID, 90*24*time.Hour)
	if err != nil {
		return problem.Internal("build ical: " + err.Error())
	}
	c.Response().Header().Set("Content-Type", "text/calendar; charset=utf-8")
	c.Response().Header().Set("Content-Disposition", `attachment; filename="sliils.ics"`)
	return c.String(http.StatusOK, body)
}

// myICalFeedURL returns the HMAC-signed subscription URL for the current
// user. Clients paste this into Apple Calendar / Google Calendar as a
// "subscribe to calendar" source; it re-fetches automatically.
func (s *Server) myICalFeedURL(c echo.Context) error {
	user := userFromContext(c)
	token := feedToken(user.ID, s.cfg.JWTSigningKey)
	// Use PublicBaseURL so the URL works from outside localhost.
	base := s.cfg.PublicBaseURL
	if base == "" {
		base = "http://localhost:8080"
	}
	return c.JSON(http.StatusOK, map[string]string{
		"url": fmt.Sprintf("%s/api/v1/feed/%d/%s/calendar.ics", base, user.ID, token),
	})
}

// icalFeed is the subscription endpoint — token-only auth.
func (s *Server) icalFeed(c echo.Context) error {
	userIDStr := c.Param("user_id")
	token := c.Param("token")
	userID, err := strconv.ParseInt(userIDStr, 10, 64)
	if err != nil {
		return problem.NotFound("not found")
	}
	if !hmac.Equal([]byte(token), []byte(feedToken(userID, s.cfg.JWTSigningKey))) {
		return problem.NotFound("not found")
	}
	body, err := s.buildUserICal(c.Request().Context(), userID, 90*24*time.Hour)
	if err != nil {
		return problem.Internal("build feed: " + err.Error())
	}
	c.Response().Header().Set("Content-Type", "text/calendar; charset=utf-8")
	return c.String(http.StatusOK, body)
}

// buildUserICal collects every event from every workspace the user belongs
// to over the given window, expands RRULE, and returns an iCal VCALENDAR.
// Single pass; no caching (90-day window is small).
func (s *Server) buildUserICal(ctx echoContext, userID int64, window time.Duration) (string, error) {
	workspaceIDs, err := s.listUserWorkspaceIDs(ctx, userID)
	if err != nil {
		return "", err
	}
	from := time.Now().Add(-24 * time.Hour)
	to := time.Now().Add(window)

	var allEvents []calendar.FeedEvent
	for _, wsID := range workspaceIDs {
		var rows []sqlcgen.ListEventsInRangeRow
		var attendeeRows []sqlcgen.ListAttendeesForEventRow
		err := db.WithTx(ctx, s.pool.Pool, db.TxOptions{UserID: userID, WorkspaceID: wsID, ReadOnly: true}, func(scope db.TxScope) error {
			r, err := scope.Queries.ListEventsInRange(ctx, sqlcgen.ListEventsInRangeParams{
				WorkspaceID: wsID,
				EndAt:       pgtype.Timestamptz{Time: from, Valid: true},
				StartAt:     pgtype.Timestamptz{Time: to, Valid: true},
			})
			if err != nil {
				return err
			}
			rows = r
			for _, row := range r {
				att, err := scope.Queries.ListAttendeesForEvent(ctx, row.ID)
				if err != nil {
					return err
				}
				attendeeRows = append(attendeeRows, att...)
			}
			return nil
		})
		if err != nil {
			return "", err
		}
		attendeeByEvent := make(map[int64][]sqlcgen.ListAttendeesForEventRow, len(rows))
		for _, a := range attendeeRows {
			attendeeByEvent[a.EventID] = append(attendeeByEvent[a.EventID], a)
		}
		for _, row := range rows {
			rrule := ""
			if row.Rrule != nil {
				rrule = *row.Rrule
			}
			// Expand RRULE so subscribed clients see every instance and
			// can render without an RRULE engine of their own. Most iCal
			// clients DO understand RRULE, but shipping expanded instances
			// dodges time-zone subtleties.
			occurrences, err := calendar.Expand(row.ID, row.StartAt.Time, row.EndAt.Time, rrule, row.TimeZone, from, to)
			if err != nil {
				continue
			}
			// For the single-instance case, emit one VEVENT with the
			// full RRULE so calendar clients that do support expansion
			// represent the series rather than a slab of independent
			// events. For non-recurring, emit each occurrence.
			if rrule != "" && len(occurrences) > 0 {
				first := occurrences[0]
				allEvents = append(allEvents, buildFeedEvent(&row, first.InstanceStart, first.InstanceEnd, rrule, attendeeByEvent[row.ID]))
			} else {
				for _, occ := range occurrences {
					allEvents = append(allEvents, buildFeedEvent(&row, occ.InstanceStart, occ.InstanceEnd, "", attendeeByEvent[row.ID]))
				}
			}
		}
	}

	body := calendar.WriteICalFeed("sliils.com", "SliilS", allEvents)
	return body, nil
}

func buildFeedEvent(row *sqlcgen.ListEventsInRangeRow, start, end time.Time, rrule string, attendees []sqlcgen.ListAttendeesForEventRow) calendar.FeedEvent {
	uid := fmt.Sprintf("sliils-event-%d@sliils.com", row.ID)
	creator := ""
	if row.CreatorDisplayName != nil {
		creator = *row.CreatorDisplayName
	}
	e := calendar.FeedEvent{
		UID:         uid,
		Summary:     row.Title,
		Description: row.Description,
		Location:    row.LocationUrl,
		Start:       start,
		End:         end,
		RRule:       rrule,
		CreatedAt:   row.CreatedAt.Time,
		UpdatedAt:   row.UpdatedAt.Time,
		Organizer:   creator,
	}
	for _, a := range attendees {
		fa := calendar.FeedAttendee{RSVP: a.Rsvp}
		if a.ExternalEmail != nil {
			fa.Email = *a.ExternalEmail
		} else if a.UserEmail != nil {
			fa.Email = *a.UserEmail
			if a.DisplayName != nil {
				fa.DisplayName = *a.DisplayName
			}
		}
		if fa.Email != "" {
			e.Attendees = append(e.Attendees, fa)
		}
	}
	return e
}

// feedToken derives an HMAC token binding user_id to the server's JWT
// signing key. The purpose string is suffixed to domain-separate from
// other HMACs that might share the same key.
func feedToken(userID int64, signingKey string) string {
	mac := hmac.New(sha256.New, []byte(signingKey))
	mac.Write([]byte(icalFeedHMACPurpose))
	mac.Write([]byte(strconv.FormatInt(userID, 10)))
	return hex.EncodeToString(mac.Sum(nil))
}

// echoContext is an alias so buildUserICal's signature reads clean while
// still accepting the standard library context.Context from handlers.
type echoContext = interface {
	Done() <-chan struct{}
	Deadline() (time.Time, bool)
	Err() error
	Value(any) any
}
