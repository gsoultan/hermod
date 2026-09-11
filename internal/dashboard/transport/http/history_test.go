package http

import (
	"testing"
	"time"
)

// The window and limit come straight off the query string, so they are
// attacker-controlled. Both feed a database query, so both need a bound: the
// point of these is that "give me everything since 1970, no limit" is not a
// request this endpoint can be talked into making.
func TestParseHistoryWindow(t *testing.T) {
	tests := []struct {
		name      string
		window    string
		limit     string
		wantSince time.Duration // age of the cutoff
		wantLimit int
	}{
		{"defaults", "", "", defaultHistoryWindow, defaultHistoryLimit},
		{"explicit window", "30m", "", 30 * time.Minute, defaultHistoryLimit},
		{"explicit limit", "", "50", defaultHistoryWindow, 50},

		// Junk is not an error worth failing a dashboard over; fall back to
		// the default rather than 400-ing a panel that was only asking for a
		// chart.
		{"unparseable window", "not-a-duration", "", defaultHistoryWindow, defaultHistoryLimit},
		{"unparseable limit", "", "abc", defaultHistoryWindow, defaultHistoryLimit},

		// Bounds. A window longer than retention can only return the retained
		// rows anyway, so clamping it costs nothing and keeps the scan bounded.
		{"window beyond retention", "9000h", "", maxHistoryWindow, defaultHistoryLimit},
		{"negative window", "-5m", "", defaultHistoryWindow, defaultHistoryLimit},
		{"zero window", "0s", "", defaultHistoryWindow, defaultHistoryLimit},
		{"limit above cap", "", "999999", defaultHistoryWindow, maxHistoryLimit},
		{"negative limit", "", "-1", defaultHistoryWindow, defaultHistoryLimit},
		{"zero limit", "", "0", defaultHistoryWindow, defaultHistoryLimit},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			before := time.Now()
			since, limit := parseHistoryWindow(tc.window, tc.limit)
			after := time.Now()

			if limit != tc.wantLimit {
				t.Errorf("limit = %d, want %d", limit, tc.wantLimit)
			}

			// since is computed from time.Now(), so assert the age lands in
			// the window the call itself spans rather than on an exact instant.
			oldest := before.Add(-tc.wantSince)
			newest := after.Add(-tc.wantSince)
			if since.Before(oldest) || since.After(newest) {
				t.Errorf("since = %v, want an age of %v (between %v and %v)",
					since, tc.wantSince, oldest, newest)
			}
		})
	}
}
