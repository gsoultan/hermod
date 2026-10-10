package telemetry

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeClock is a race-safe clock a test advances by hand.
type fakeClock struct{ nanos atomic.Int64 }

func newFakeClock() *fakeClock {
	c := &fakeClock{}
	c.nanos.Store(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixNano())
	return c
}

func (c *fakeClock) Now() time.Time           { return time.Unix(0, c.nanos.Load()) }
func (c *fakeClock) Advance(d time.Duration) { c.nanos.Add(int64(d)) }

// GetMPS used to reset the counter it read (Swap(0)). The status is read from
// several goroutines — the status listener, the flusher, the dashboard sampler,
// the optimizer — so two of them reaching a second boundary together split one
// second's messages between them, and whichever got the empty half published a
// throughput of 0 for the rest of that second.
func TestGetMPSConcurrentReadersAllSeeTheSameRate(t *testing.T) {
	clk := newFakeClock()
	s := NewStatusTracker()
	s.now = clk.Now

	const perSecond = 50
	const readers = 8

	_ = s.GetMPS() // first observation: nothing to compare against yet
	for second := range 300 {
		for range perSecond {
			s.IncProcessed()
		}
		clk.Advance(time.Second)

		var start, done sync.WaitGroup
		start.Add(1)
		got := make([]float64, readers)
		for i := range readers {
			done.Go(func() {
				start.Wait()
				got[i] = s.GetMPS()
			})
		}
		start.Done()
		done.Wait()

		for i, v := range got {
			if v != perSecond {
				t.Fatalf("second %d: reader %d saw %.1f msg/s; want %d (all readers: %v)", second, i, v, perSecond, got)
			}
		}
	}
}

// Nothing reads the status on an exact one-second cadence. A reader that came
// back after more than a second was told the gap "had 0 throughput" and got 0,
// however steadily messages were flowing.
func TestGetMPSReadsFurtherApartThanOneSecondReportTheRate(t *testing.T) {
	clk := newFakeClock()
	s := NewStatusTracker()
	s.now = clk.Now

	_ = s.GetMPS()
	for i := range 5 {
		// 100 msg/s for two seconds between reads.
		for range 200 {
			s.IncProcessed()
		}
		clk.Advance(2 * time.Second)
		if got := s.GetMPS(); got != 100 {
			t.Fatalf("read %d: GetMPS = %.1f; want 100 msg/s", i, got)
		}
	}
}

// Within a sampling window every read reports the same completed-window rate,
// and reading does not consume anything.
func TestGetMPSRepeatedReadsDoNotChangeTheAnswer(t *testing.T) {
	clk := newFakeClock()
	s := NewStatusTracker()
	s.now = clk.Now

	_ = s.GetMPS()
	for range 40 {
		s.IncProcessed()
	}
	clk.Advance(time.Second)
	first := s.GetMPS()
	clk.Advance(300 * time.Millisecond)
	second := s.GetMPS()
	if first != 40 || second != 40 {
		t.Fatalf("GetMPS = %.1f then %.1f within one window; want 40 both times", first, second)
	}
	if got := s.processedMessages.Load(); got != 40 {
		t.Fatalf("processed count = %d after reads; reading must not change it", got)
	}
}

// An idle workflow reports zero once a full window has passed with nothing.
func TestGetMPSIdleReportsZero(t *testing.T) {
	clk := newFakeClock()
	s := NewStatusTracker()
	s.now = clk.Now

	_ = s.GetMPS()
	for range 10 {
		s.IncProcessed()
	}
	clk.Advance(time.Second)
	if got := s.GetMPS(); got != 10 {
		t.Fatalf("GetMPS = %.1f; want 10", got)
	}
	clk.Advance(time.Second)
	if got := s.GetMPS(); got != 0 {
		t.Fatalf("GetMPS = %.1f after an idle second; want 0", got)
	}
}
