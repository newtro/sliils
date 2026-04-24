package calendar

import (
	"testing"
	"time"
)

func TestExpandSingleEvent(t *testing.T) {
	loc := time.UTC
	start := time.Date(2026, 5, 1, 10, 0, 0, 0, loc)
	end := start.Add(30 * time.Minute)

	tests := []struct {
		name       string
		windowFrom time.Time
		windowTo   time.Time
		want       int
	}{
		{"inside window", start.Add(-1 * time.Hour), start.Add(2 * time.Hour), 1},
		{"before window", start.Add(10 * time.Hour), start.Add(20 * time.Hour), 0},
		{"after window", start.Add(-10 * time.Hour), start.Add(-5 * time.Hour), 0},
		{"straddles start", start.Add(15 * time.Minute), start.Add(2 * time.Hour), 1},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := Expand(1, start, end, "", "UTC", tt.windowFrom, tt.windowTo)
			if err != nil {
				t.Fatalf("Expand err: %v", err)
			}
			if len(got) != tt.want {
				t.Fatalf("got %d occurrences, want %d", len(got), tt.want)
			}
		})
	}
}

func TestExpandWeekly(t *testing.T) {
	// Every Monday 10:00 UTC starting 2026-05-04 (a Monday).
	loc := time.UTC
	start := time.Date(2026, 5, 4, 10, 0, 0, 0, loc)
	end := start.Add(30 * time.Minute)

	// Query window is 20 days from the start Monday — captures exactly
	// three Mondays (day 0, 7, 14); the fourth lands on day 21 which is
	// outside the inclusive upper bound.
	got, err := Expand(42, start, end, "FREQ=WEEKLY;BYDAY=MO", "UTC", start, start.Add(20*24*time.Hour))
	if err != nil {
		t.Fatalf("Expand err: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d, want 3 Mondays; got=%v", len(got), got)
	}
	// Every occurrence is exactly 7 days after the prior.
	for i := 1; i < len(got); i++ {
		diff := got[i].InstanceStart.Sub(got[i-1].InstanceStart)
		if diff != 7*24*time.Hour {
			t.Fatalf("occurrence %d gap %s, want 168h", i, diff)
		}
	}
	if got[0].EventID != 42 {
		t.Fatalf("event id not carried through, got %d", got[0].EventID)
	}
}

func TestExpandDailyWithCount(t *testing.T) {
	loc := time.UTC
	start := time.Date(2026, 6, 1, 9, 0, 0, 0, loc)
	end := start.Add(15 * time.Minute)

	// COUNT limits total instances even if the window is wider.
	got, err := Expand(7, start, end, "FREQ=DAILY;COUNT=5", "UTC", start, start.Add(30*24*time.Hour))
	if err != nil {
		t.Fatalf("Expand err: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("COUNT=5 should cap at 5; got %d", len(got))
	}
}

func TestExpandSafetyLimit(t *testing.T) {
	// Every minute for 10 years should get clipped at MaxOccurrencesPerQuery.
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(1 * time.Minute)
	got, err := Expand(9, start, end, "FREQ=MINUTELY", "UTC",
		start, start.Add(10*365*24*time.Hour))
	if err != nil {
		t.Fatalf("Expand err: %v", err)
	}
	if len(got) > MaxOccurrencesPerQuery {
		t.Fatalf("expected at most %d occurrences, got %d", MaxOccurrencesPerQuery, len(got))
	}
}
