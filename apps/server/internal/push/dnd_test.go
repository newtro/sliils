package push

import (
	"testing"
	"time"
)

func ptrInt(n int) *int { return &n }

func TestQuietHoursSimpleWindow(t *testing.T) {
	// 13:00-14:00 UTC — quiet window during afternoon.
	d := DNDState{
		QuietHoursStart: ptrInt(13 * 60),
		QuietHoursEnd:   ptrInt(14 * 60),
		TimeZone:        "UTC",
	}
	now := time.Date(2026, 4, 24, 13, 30, 0, 0, time.UTC)
	if !d.InQuietHours(now) {
		t.Fatalf("expected 13:30 UTC to be in quiet window 13:00-14:00")
	}
	outside := time.Date(2026, 4, 24, 10, 0, 0, 0, time.UTC)
	if d.InQuietHours(outside) {
		t.Fatalf("expected 10:00 UTC to be outside 13:00-14:00")
	}
}

func TestQuietHoursWrapsMidnight(t *testing.T) {
	// 22:00-08:00 UTC — overnight window.
	d := DNDState{
		QuietHoursStart: ptrInt(22 * 60),
		QuietHoursEnd:   ptrInt(8 * 60),
		TimeZone:        "UTC",
	}
	cases := []struct {
		hour, minute int
		want         bool
	}{
		{23, 0, true},  // late evening
		{2, 30, true},  // early morning
		{7, 59, true},  // just before end
		{8, 0, false},  // on end boundary
		{12, 0, false}, // midday
		{21, 59, false},
		{22, 0, true},
	}
	for _, c := range cases {
		now := time.Date(2026, 4, 24, c.hour, c.minute, 0, 0, time.UTC)
		got := d.InQuietHours(now)
		if got != c.want {
			t.Errorf("%02d:%02d: got %v want %v", c.hour, c.minute, got, c.want)
		}
	}
}

func TestSnoozeEnabledUntil(t *testing.T) {
	now := time.Date(2026, 4, 24, 10, 0, 0, 0, time.UTC)
	d := DNDState{EnabledUntil: now.Add(1 * time.Hour)}
	if !d.ShouldSuppress(now) {
		t.Fatalf("active snooze should suppress")
	}
	if d.ShouldSuppress(now.Add(2 * time.Hour)) {
		t.Fatalf("expired snooze should not suppress")
	}
}

func TestDegenerateWindowDoesNotSuppress(t *testing.T) {
	// start == end is meaningless; treat as no window.
	d := DNDState{
		QuietHoursStart: ptrInt(600),
		QuietHoursEnd:   ptrInt(600),
	}
	if d.InQuietHours(time.Now()) {
		t.Fatalf("start==end should not suppress")
	}
}

func TestBadTimezoneFallsBackToUTC(t *testing.T) {
	// Nonsense TZ — must not panic; must fall back to UTC.
	d := DNDState{
		QuietHoursStart: ptrInt(13 * 60),
		QuietHoursEnd:   ptrInt(14 * 60),
		TimeZone:        "Mars/Olympus_Mons",
	}
	now := time.Date(2026, 4, 24, 13, 30, 0, 0, time.UTC)
	if !d.InQuietHours(now) {
		t.Fatalf("unknown tz should still match via UTC fallback")
	}
}
