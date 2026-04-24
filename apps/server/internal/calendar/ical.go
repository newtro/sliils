package calendar

import (
	"fmt"
	"strings"
	"time"
)

// iCal (RFC 5545) serializer for SliilS events. Scope: minimal VEVENTs
// that Apple Calendar / Google / Outlook can all read. Full iCal with
// VTIMEZONE blocks is overkill for the M9 export surface — we stamp
// every time in UTC so no TZID is needed.
//
// Public entrypoint: WriteICalFeed(w, events).

// FeedEvent is the export view of an event. Keep it decoupled from the
// sqlcgen row shape so the handler can populate it however it wants.
type FeedEvent struct {
	UID         string // stable unique identifier
	Summary     string
	Description string
	Location    string
	Start       time.Time
	End         time.Time
	RRule       string // bare RRULE value (no "RRULE:" prefix) — empty for single instance
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Organizer   string // "Name <email>" — optional
	Attendees   []FeedAttendee
}

type FeedAttendee struct {
	Email       string
	DisplayName string
	RSVP        string // "yes" | "no" | "maybe" | "pending"
}

// WriteICalFeed builds a VCALENDAR document from the provided events.
// Returns the text body ready to ship via HTTP with Content-Type
// text/calendar.
func WriteICalFeed(productID, calName string, events []FeedEvent) string {
	var b strings.Builder
	b.WriteString("BEGIN:VCALENDAR\r\n")
	b.WriteString("VERSION:2.0\r\n")
	b.WriteString("PRODID:-//" + productID + "//EN\r\n")
	b.WriteString("CALSCALE:GREGORIAN\r\n")
	if calName != "" {
		b.WriteString("X-WR-CALNAME:" + escapeICalText(calName) + "\r\n")
	}
	for _, e := range events {
		writeVEvent(&b, e)
	}
	b.WriteString("END:VCALENDAR\r\n")
	return b.String()
}

func writeVEvent(b *strings.Builder, e FeedEvent) {
	b.WriteString("BEGIN:VEVENT\r\n")
	fmt.Fprintf(b, "UID:%s\r\n", e.UID)
	fmt.Fprintf(b, "DTSTAMP:%s\r\n", fmtUTC(e.UpdatedAt))
	fmt.Fprintf(b, "DTSTART:%s\r\n", fmtUTC(e.Start))
	fmt.Fprintf(b, "DTEND:%s\r\n", fmtUTC(e.End))
	if e.Summary != "" {
		fmt.Fprintf(b, "SUMMARY:%s\r\n", escapeICalText(e.Summary))
	}
	if e.Description != "" {
		fmt.Fprintf(b, "DESCRIPTION:%s\r\n", escapeICalText(e.Description))
	}
	if e.Location != "" {
		fmt.Fprintf(b, "LOCATION:%s\r\n", escapeICalText(e.Location))
	}
	if e.RRule != "" {
		fmt.Fprintf(b, "RRULE:%s\r\n", e.RRule)
	}
	if !e.CreatedAt.IsZero() {
		fmt.Fprintf(b, "CREATED:%s\r\n", fmtUTC(e.CreatedAt))
	}
	if e.Organizer != "" {
		fmt.Fprintf(b, "ORGANIZER:%s\r\n", e.Organizer)
	}
	for _, a := range e.Attendees {
		writeAttendee(b, a)
	}
	b.WriteString("END:VEVENT\r\n")
}

func writeAttendee(b *strings.Builder, a FeedAttendee) {
	partStat := mapRSVPToPartStat(a.RSVP)
	if a.DisplayName != "" {
		fmt.Fprintf(b, "ATTENDEE;CN=%s;PARTSTAT=%s:mailto:%s\r\n",
			escapeICalText(a.DisplayName), partStat, a.Email)
	} else {
		fmt.Fprintf(b, "ATTENDEE;PARTSTAT=%s:mailto:%s\r\n", partStat, a.Email)
	}
}

func mapRSVPToPartStat(rsvp string) string {
	switch rsvp {
	case "yes":
		return "ACCEPTED"
	case "no":
		return "DECLINED"
	case "maybe":
		return "TENTATIVE"
	default:
		return "NEEDS-ACTION"
	}
}

func fmtUTC(t time.Time) string {
	return t.UTC().Format("20060102T150405Z")
}

// escapeICalText handles the three special characters in RFC 5545 TEXT
// values: backslash, comma, semicolon, plus CRLF. Not a full
// RFC-compliant content-line folder — that's a future concern when we
// start emitting long descriptions.
func escapeICalText(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, ",", "\\,")
	s = strings.ReplaceAll(s, ";", "\\;")
	s = strings.ReplaceAll(s, "\r\n", "\\n")
	s = strings.ReplaceAll(s, "\n", "\\n")
	return s
}
