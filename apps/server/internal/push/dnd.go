package push

import (
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// DNDState captures everything we need to decide "should this user get
// a push right now?" at a fixed point in time. Kept as a plain struct
// so the fan-out worker can hydrate once and evaluate for every
// candidate recipient.
type DNDState struct {
	// EnabledUntil: explicit "mute everything until T" set via the
	// snooze UI. Zero-value = no snooze.
	EnabledUntil time.Time

	// QuietHoursStart / End are "minutes since midnight" in the user's
	// local timezone. nil = quiet hours disabled.
	QuietHoursStart *int
	QuietHoursEnd   *int
	// TimeZone is an IANA name. Empty = UTC (acceptable fallback when
	// the user hasn't set one).
	TimeZone string
}

// InQuietHours reports whether `now` lies inside the user's configured
// quiet-hours window. Handles ranges that wrap midnight (e.g. 22:00→08:00).
// Returns false if quiet hours aren't configured.
func (d DNDState) InQuietHours(now time.Time) bool {
	if d.QuietHoursStart == nil || d.QuietHoursEnd == nil {
		return false
	}
	loc := time.UTC
	if d.TimeZone != "" {
		if tz, err := time.LoadLocation(d.TimeZone); err == nil {
			loc = tz
		}
	}
	local := now.In(loc)
	nowMin := local.Hour()*60 + local.Minute()
	start := *d.QuietHoursStart
	end := *d.QuietHoursEnd
	if start == end {
		return false // degenerate — treat as "no quiet hours"
	}
	if start < end {
		return nowMin >= start && nowMin < end
	}
	// Wraps midnight: active when nowMin >= start OR nowMin < end.
	return nowMin >= start || nowMin < end
}

// ShouldSuppress is the top-level gate. True = suppress; false = deliver.
func (d DNDState) ShouldSuppress(now time.Time) bool {
	if !d.EnabledUntil.IsZero() && now.Before(d.EnabledUntil) {
		return true
	}
	return d.InQuietHours(now)
}

// StateFromRow adapts the sqlcgen row into a DNDState. Accepts the
// pgtype.Timestamptz directly so callers don't have to unwrap Valid.
func StateFromRow(enabledUntil pgtype.Timestamptz, qhStart, qhEnd *int32, tz *string) DNDState {
	out := DNDState{}
	if enabledUntil.Valid {
		out.EnabledUntil = enabledUntil.Time
	}
	if qhStart != nil {
		v := int(*qhStart)
		out.QuietHoursStart = &v
	}
	if qhEnd != nil {
		v := int(*qhEnd)
		out.QuietHoursEnd = &v
	}
	if tz != nil {
		out.TimeZone = *tz
	}
	return out
}
