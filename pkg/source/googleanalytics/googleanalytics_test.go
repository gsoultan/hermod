package googleanalytics

import (
	"testing"
	"time"
)

func TestGoogleAnalyticsSource_State(t *testing.T) {
	s := NewGoogleAnalyticsSource("123", "{}", "metrics", "dimensions", 1*time.Hour)

	now := time.Now().Truncate(time.Second)
	state := map[string]string{
		"last_fetch": now.Format(time.RFC3339),
	}

	s.SetState(state)

	if !s.lastFetch.Equal(now) {
		t.Errorf("Expected lastFetch %v, got %v", now, s.lastFetch)
	}

	newState := s.GetState()
	if newState["last_fetch"] != state["last_fetch"] {
		t.Errorf("Expected state last_fetch %s, got %s", state["last_fetch"], newState["last_fetch"])
	}
}

func TestNewGoogleAnalyticsSource(t *testing.T) {
	s := NewGoogleAnalyticsSource("123", "{}", "metrics", "dimensions", 0)
	if s.pollInterval != 1*time.Hour {
		t.Errorf("Expected default poll interval 1h, got %v", s.pollInterval)
	}
}
