package config

import (
	"testing"
	"time"
)

// The recovery thresholds decide when a workflow is declared wedged and
// rebuilt. They were reachable only by recompiling: Registry.SetConfig existed
// but nothing called it, so every deployment ran the 60s default whatever its
// pipelines looked like.
//
// That is not a tuning nicety. A workflow whose sink is legitimately slow needs
// a longer threshold or it is restarted while it is working; an operator
// responding to an incident needs to shorten it without a release. Both are
// operational levers, so they belong in the environment alongside
// HERMOD_LOG_LEVEL.
func TestDefaultConfigReadsRecoveryThresholdsFromEnv(t *testing.T) {
	t.Run("stall threshold", func(t *testing.T) {
		t.Setenv("HERMOD_STALL_THRESHOLD", "5m")
		if got := DefaultConfig().StallThreshold; got != 5*time.Minute {
			t.Errorf("StallThreshold = %v, want 5m", got)
		}
	})

	t.Run("stream silence sampling interval", func(t *testing.T) {
		t.Setenv("HERMOD_STREAM_SILENCE_INTERVAL", "30s")
		if got := DefaultConfig().StreamSilenceInterval; got != 30*time.Second {
			t.Errorf("StreamSilenceInterval = %v, want 30s", got)
		}
	})

	t.Run("lag warning threshold", func(t *testing.T) {
		t.Setenv("HERMOD_LAG_WARN_BYTES", "1048576")
		if got := DefaultConfig().LagWarnBytes; got != 1<<20 {
			t.Errorf("LagWarnBytes = %d, want %d", got, 1<<20)
		}
	})

	t.Run("an unset variable keeps the default", func(t *testing.T) {
		if got := DefaultConfig().StallThreshold; got != 60*time.Second {
			t.Errorf("StallThreshold = %v, want the 60s default", got)
		}
	})

	t.Run("a malformed value keeps the default rather than disabling detection", func(t *testing.T) {
		// An empty or zero threshold would switch stall detection off entirely.
		// A typo in a deployment variable must not silently do that.
		for _, bad := range []string{"", "soon", "-1s", "0"} {
			t.Setenv("HERMOD_STALL_THRESHOLD", bad)
			if got := DefaultConfig().StallThreshold; got != 60*time.Second {
				t.Errorf("HERMOD_STALL_THRESHOLD=%q gave %v, want the 60s default: a bad value must not disable stall detection", bad, got)
			}
		}
	})
}
