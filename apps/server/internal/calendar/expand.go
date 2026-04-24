// Package calendar owns RRULE expansion and iCal serialization for M9.
//
// The server stores each event series as a single row: start_at +
// end_at pin the first occurrence, rrule carries the recurrence string.
// When a client asks for events in a window, we:
//
//   1. SELECT candidate series via ListEventsInRange (start_at <= window
//      end, plus a cheap rrule-or-end-in-range filter).
//   2. Expand each series against the window using rrule-go. Single
//      events short-circuit — no library call if rrule is null.
//   3. Each expanded occurrence carries back-refs (event_id, series_id,
//      instance_start) so clients can keep stable keys across refreshes.
//
// Expansion caps at a per-query instance limit so a malicious RRULE
// ("every minute forever") can't pin the CPU.
package calendar

import (
	"fmt"
	"time"

	"github.com/teambition/rrule-go"
)

// MaxOccurrencesPerQuery is a safety limit on how many instances we'll
// materialize from a single RRULE for one window. A year of every-5-min
// events is ~105k — way beyond any calendar UI's needs. 2k is plenty.
const MaxOccurrencesPerQuery = 2000

// Occurrence is one instance of a series (or the lone instance of a
// non-recurring event). Stable across refreshes because InstanceStart
// is derived from RRULE expansion in a deterministic way.
type Occurrence struct {
	EventID       int64
	InstanceStart time.Time
	InstanceEnd   time.Time
}

// Expand materializes every instance of a series that overlaps the given
// window. Both bounds are inclusive (we clip at the query-range edge).
// Returns a sorted slice by start time.
//
// baseStart/baseEnd define the first occurrence's span; durationTz is
// the event's IANA time zone so DST transitions produce sensible
// local-time instances (an event at 9am local stays at 9am local
// after the clocks change).
//
// rrule may be empty — the function returns a one-element slice if the
// single instance overlaps the window.
func Expand(
	eventID int64,
	baseStart, baseEnd time.Time,
	rruleStr, timeZone string,
	windowFrom, windowTo time.Time,
) ([]Occurrence, error) {
	duration := baseEnd.Sub(baseStart)
	if duration < 0 {
		return nil, fmt.Errorf("event end_at is before start_at")
	}

	if rruleStr == "" {
		if occursInWindow(baseStart, baseEnd, windowFrom, windowTo) {
			return []Occurrence{{EventID: eventID, InstanceStart: baseStart, InstanceEnd: baseEnd}}, nil
		}
		return nil, nil
	}

	loc, err := time.LoadLocation(timeZone)
	if err != nil {
		// Fall back to UTC rather than rejecting the whole series — a
		// typo'd TZ shouldn't knock the entire calendar out.
		loc = time.UTC
	}

	// rrule-go wants a slice of lines: ["DTSTART...", "RRULE:..."] —
	// one element per iCalendar line, no embedded newlines.
	lines := buildRRuleLines(baseStart.In(loc), rruleStr)
	set, err := rrule.StrSliceToRRuleSet(lines)
	if err != nil {
		return nil, fmt.Errorf("parse rrule: %w", err)
	}

	// Between() returns start times in the (after, before, inc) window.
	// We shift windowFrom back by `duration` so an event that started
	// BEFORE windowFrom but is still running during it gets included.
	starts := set.Between(windowFrom.Add(-duration), windowTo, true)

	out := make([]Occurrence, 0, len(starts))
	for i, s := range starts {
		if i >= MaxOccurrencesPerQuery {
			break
		}
		end := s.Add(duration)
		if !occursInWindow(s, end, windowFrom, windowTo) {
			continue
		}
		out = append(out, Occurrence{EventID: eventID, InstanceStart: s, InstanceEnd: end})
	}
	return out, nil
}

// buildRRuleLines produces the iCalendar line slice rrule-go's
// StrSliceToRRuleSet expects: a DTSTART line, then a RRULE line.
//
// DTSTART formats per RFC 5545:
//   - UTC:   "DTSTART:20260501T100000Z"            (trailing Z)
//   - Local: "DTSTART;TZID=America/New_York:..."   (no trailing Z)
//
// Location().String() returns the IANA name (e.g. "America/New_York");
// time.Zone() returns the abbreviation (e.g. "EDT") which IS NOT what
// rrule-go wants.
func buildRRuleLines(dtstart time.Time, rruleStr string) []string {
	var dtLine string
	if dtstart.Location() == time.UTC || dtstart.Location().String() == "UTC" {
		dtLine = "DTSTART:" + dtstart.UTC().Format("20060102T150405") + "Z"
	} else {
		dtLine = fmt.Sprintf("DTSTART;TZID=%s:%s",
			dtstart.Location().String(),
			dtstart.Format("20060102T150405"),
		)
	}
	return []string{dtLine, "RRULE:" + trimRRulePrefix(rruleStr)}
}

// trimRRulePrefix removes a leading "RRULE:" if the user typed it — the
// envelope builder adds its own.
func trimRRulePrefix(s string) string {
	if len(s) >= 6 && (s[:6] == "RRULE:" || s[:6] == "rrule:") {
		return s[6:]
	}
	return s
}

// occursInWindow is a closed-interval overlap check. The event's span
// ([start, end]) must touch the window ([from, to]) in at least one
// point for a UI to render it.
func occursInWindow(start, end, from, to time.Time) bool {
	if end.Before(from) {
		return false
	}
	if start.After(to) {
		return false
	}
	return true
}
