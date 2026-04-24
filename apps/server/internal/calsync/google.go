package calsync

import (
	"context"
	"errors"
	"fmt"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	calsvc "google.golang.org/api/calendar/v3"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

// Google provider. We target the user's primary calendar — a multi-
// calendar picker is a polish item. `AccessType=offline` is mandatory or
// Google won't return a refresh token; `Prompt=consent` forces the
// consent screen even on re-auth so we reliably get a fresh refresh
// token each time the user reconnects.
type Google struct {
	oauth *oauth2.Config
}

// NewGoogle builds a Google provider. Pass your OAuth console
// credentials; the redirect URL must exactly match one registered
// there.
func NewGoogle(clientID, clientSecret, redirectURL string) *Google {
	return &Google{
		oauth: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURL,
			Endpoint:     google.Endpoint,
			Scopes: []string{
				calsvc.CalendarEventsScope,
				"https://www.googleapis.com/auth/userinfo.email",
			},
		},
	}
}

func (g *Google) Name() string { return "google" }

func (g *Google) AuthCodeURL(state string) string {
	return g.oauth.AuthCodeURL(state,
		oauth2.AccessTypeOffline,
		oauth2.ApprovalForce, // == prompt=consent; ensures refresh_token
	)
}

func (g *Google) Exchange(ctx context.Context, code string) (*oauth2.Token, error) {
	tok, err := g.oauth.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("google exchange: %w", err)
	}
	if tok.RefreshToken == "" {
		return nil, errors.New("google did not return a refresh token — consent must include 'offline' access")
	}
	return tok, nil
}

func (g *Google) AccountEmail(ctx context.Context, refreshToken string) (string, error) {
	ts := g.tokenSource(ctx, refreshToken)
	svc, err := calsvc.NewService(ctx, option.WithTokenSource(ts))
	if err != nil {
		return "", err
	}
	// The primary calendar's id IS the user's email in Google Calendar.
	cal, err := svc.CalendarList.Get("primary").Context(ctx).Do()
	if err != nil {
		return "", maybeReauth(err)
	}
	return cal.Id, nil
}

// Pull: incremental sync via syncToken. When syncToken is empty we do
// an initial seed pull (1-year window) and return the next cursor.
func (g *Google) Pull(ctx context.Context, refreshToken, syncToken string) (*PullResult, error) {
	ts := g.tokenSource(ctx, refreshToken)
	svc, err := calsvc.NewService(ctx, option.WithTokenSource(ts))
	if err != nil {
		return nil, err
	}

	var result PullResult
	pageToken := ""
	for {
		call := svc.Events.List("primary").
			SingleEvents(false). // keep RRULE series intact — we store the rule, not instances
			ShowDeleted(true).
			Context(ctx)
		if syncToken != "" {
			call = call.SyncToken(syncToken)
		} else {
			// First sync: pull a year forward so the calendar has content.
			call = call.TimeMin(time.Now().Add(-30 * 24 * time.Hour).Format(time.RFC3339)).
				TimeMax(time.Now().Add(365 * 24 * time.Hour).Format(time.RFC3339))
		}
		if pageToken != "" {
			call = call.PageToken(pageToken)
		}
		resp, err := call.Do()
		if err != nil {
			// 410 Gone = sync token invalidated; fall back to full resync
			// by clearing the token. The worker will pick up with a fresh
			// seed next tick.
			if ge, ok := err.(*googleapi.Error); ok && ge.Code == 410 {
				return &PullResult{IncrementalCursor: ""}, nil
			}
			return nil, maybeReauth(err)
		}

		for _, ev := range resp.Items {
			ce := ChangedEvent{ExternalID: ev.Id, ETag: ev.Etag}
			if ev.Status == "cancelled" {
				ce.Deleted = true
			} else {
				converted, err := convertFromGoogle(ev)
				if err != nil {
					continue
				}
				ce.Event = converted
			}
			result.Changed = append(result.Changed, ce)
		}
		if resp.NextSyncToken != "" {
			result.IncrementalCursor = resp.NextSyncToken
		}
		if resp.NextPageToken == "" {
			break
		}
		pageToken = resp.NextPageToken
	}
	return &result, nil
}

func (g *Google) Push(ctx context.Context, refreshToken string, evt *Event, existingID string) (string, string, error) {
	ts := g.tokenSource(ctx, refreshToken)
	svc, err := calsvc.NewService(ctx, option.WithTokenSource(ts))
	if err != nil {
		return "", "", err
	}
	body := convertToGoogle(evt)
	if existingID == "" {
		created, err := svc.Events.Insert("primary", body).Context(ctx).Do()
		if err != nil {
			return "", "", maybeReauth(err)
		}
		return created.Id, created.Etag, nil
	}
	updated, err := svc.Events.Update("primary", existingID, body).Context(ctx).Do()
	if err != nil {
		return "", "", maybeReauth(err)
	}
	return updated.Id, updated.Etag, nil
}

func (g *Google) Delete(ctx context.Context, refreshToken, externalID string) error {
	ts := g.tokenSource(ctx, refreshToken)
	svc, err := calsvc.NewService(ctx, option.WithTokenSource(ts))
	if err != nil {
		return err
	}
	if err := svc.Events.Delete("primary", externalID).Context(ctx).Do(); err != nil {
		return maybeReauth(err)
	}
	return nil
}

// ---- helpers ----------------------------------------------------------

func (g *Google) tokenSource(ctx context.Context, refreshToken string) oauth2.TokenSource {
	// oauth2 refreshes automatically when we pass a token with RefreshToken
	// set; AccessToken empty means "please refresh on first use".
	return g.oauth.TokenSource(ctx, &oauth2.Token{RefreshToken: refreshToken})
}

func maybeReauth(err error) error {
	ge, ok := err.(*googleapi.Error)
	if !ok {
		return err
	}
	if ge.Code == 401 || ge.Code == 403 {
		return fmt.Errorf("%w: %s", ErrNeedsReauth, ge.Message)
	}
	return err
}

func convertToGoogle(evt *Event) *calsvc.Event {
	body := &calsvc.Event{
		Summary:     evt.Title,
		Description: evt.Description,
		Location:    evt.Location,
		Start: &calsvc.EventDateTime{
			DateTime: evt.Start.Format(time.RFC3339),
			TimeZone: evt.TimeZone,
		},
		End: &calsvc.EventDateTime{
			DateTime: evt.End.Format(time.RFC3339),
			TimeZone: evt.TimeZone,
		},
	}
	if evt.RRule != "" {
		body.Recurrence = []string{"RRULE:" + evt.RRule}
	}
	for _, a := range evt.Attendees {
		att := &calsvc.EventAttendee{Email: a.Email, DisplayName: a.DisplayName}
		switch a.RSVP {
		case "yes":
			att.ResponseStatus = "accepted"
		case "no":
			att.ResponseStatus = "declined"
		case "maybe":
			att.ResponseStatus = "tentative"
		default:
			att.ResponseStatus = "needsAction"
		}
		body.Attendees = append(body.Attendees, att)
	}
	return body
}

func convertFromGoogle(ev *calsvc.Event) (*Event, error) {
	if ev.Start == nil || ev.End == nil {
		return nil, errors.New("event missing start or end")
	}
	start, err := parseGoogleTime(ev.Start)
	if err != nil {
		return nil, err
	}
	end, err := parseGoogleTime(ev.End)
	if err != nil {
		return nil, err
	}
	out := &Event{
		Title:       ev.Summary,
		Description: ev.Description,
		Location:    ev.Location,
		Start:       start,
		End:         end,
		TimeZone:    firstTZ(ev.Start.TimeZone, ev.End.TimeZone),
	}
	for _, r := range ev.Recurrence {
		// Recurrence carries zero or more lines: RRULE, EXDATE, etc. We
		// only extract the RRULE and drop exceptions for v1.
		if len(r) > 6 && r[:6] == "RRULE:" {
			out.RRule = r[6:]
		}
	}
	for _, a := range ev.Attendees {
		rsvp := "pending"
		switch a.ResponseStatus {
		case "accepted":
			rsvp = "yes"
		case "declined":
			rsvp = "no"
		case "tentative":
			rsvp = "maybe"
		}
		out.Attendees = append(out.Attendees, Attendee{
			Email: a.Email, DisplayName: a.DisplayName, RSVP: rsvp,
		})
	}
	return out, nil
}

func parseGoogleTime(edt *calsvc.EventDateTime) (time.Time, error) {
	if edt.DateTime != "" {
		return time.Parse(time.RFC3339, edt.DateTime)
	}
	if edt.Date != "" {
		// All-day events. We'd represent these separately in a richer
		// model; for v1 anchor at midnight in the event's tz.
		return time.Parse("2006-01-02", edt.Date)
	}
	return time.Time{}, errors.New("neither DateTime nor Date on Google event")
}

func firstTZ(opts ...string) string {
	for _, o := range opts {
		if o != "" {
			return o
		}
	}
	return "UTC"
}
