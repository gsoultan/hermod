package postgres

import (
	"testing"
	"time"

	"github.com/gsoultan/Hermod"
)

// The silence deadline is derived from the server's own keepalive cadence
// rather than assumed. PostgreSQL sends a keepalive on an idle logical
// replication connection every wal_sender_timeout/2, so a server configured
// with a long timeout is legitimately quiet for a long time — a hardcoded
// deadline would declare those streams dead and restart healthy workflows in a
// loop.
func TestStreamSilenceThresholdFollowsWalSenderTimeout(t *testing.T) {
	tests := []struct {
		name             string
		useCDC           bool
		walSenderTimeout time.Duration
		want             time.Duration
	}{
		{
			// The dev database's default, confirmed via pg_settings.
			name:             "the 60s default gives a 90s deadline",
			useCDC:           true,
			walSenderTimeout: 60 * time.Second,
			want:             90 * time.Second,
		},
		{
			name:             "a long timeout widens the deadline instead of firing early",
			useCDC:           true,
			walSenderTimeout: 10 * time.Minute,
			want:             15 * time.Minute,
		},
		{
			name:             "keepalives disabled means the check is disabled",
			useCDC:           true,
			walSenderTimeout: 0,
			want:             0,
		},
		{
			name:             "an unread timeout leaves the check disabled",
			useCDC:           true,
			walSenderTimeout: -1,
			want:             0,
		},
		{
			name:             "a non-CDC source has no stream to watch",
			useCDC:           false,
			walSenderTimeout: 60 * time.Second,
			want:             0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := &PostgresSource{useCDC: tc.useCDC}
			p.walSenderTimeout.Store(int64(tc.walSenderTimeout))

			if got := p.StreamSilenceThreshold(); got != tc.want {
				t.Errorf("StreamSilenceThreshold() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestLastStreamActivity(t *testing.T) {
	t.Run("a stream that has never received anything reports zero", func(t *testing.T) {
		p := &PostgresSource{useCDC: true}
		if got := p.LastStreamActivity(); !got.IsZero() {
			t.Errorf("LastStreamActivity() = %v, want the zero time so a stream that has not started is not called wedged", got)
		}
	})

	t.Run("recorded activity is reported", func(t *testing.T) {
		p := &PostgresSource{useCDC: true}
		before := time.Now()
		p.noteStreamActivity()

		got := p.LastStreamActivity()
		if got.Before(before) {
			t.Errorf("LastStreamActivity() = %v, want at or after %v", got, before)
		}
	})
}

// The engine only watches a source's stream if the source advertises that it
// has one. A PostgresSource that stopped satisfying this interface would lose
// the check silently, exactly as it once lost GetLag behind a wrapper.
func TestPostgresSourceReportsStreamLiveness(t *testing.T) {
	var p any = &PostgresSource{useCDC: true}
	if _, ok := p.(hermod.StreamLivenessReporter); !ok {
		t.Fatal("PostgresSource no longer implements hermod.StreamLivenessReporter: a wedged replication stream would go unnoticed")
	}
}
