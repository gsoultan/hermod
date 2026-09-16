package pubsub

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestPingSeparatesAMissingTopicFromAnUnreachableServer covers the branch the
// v2 migration had to write by hand.
//
// v1 answered "does this topic exist" with Topic.Exists, which returned a bool.
// v2 dropped it, so existence is now an admin-API call whose NotFound has to be
// told apart from every other failure: a missing topic is a configuration
// mistake an operator can fix, while an unreachable endpoint is the sink being
// unable to reach Pub/Sub at all. Reporting the second as the first sends
// whoever is on call to the wrong place.
//
// No broker is needed: an address nothing is listening on exercises the
// not-NotFound branch, which is the one that used to be impossible to reach.
func TestPingSeparatesAMissingTopicFromAnUnreachableServer(t *testing.T) {
	t.Setenv("PUBSUB_EMULATOR_HOST", "127.0.0.1:1") // nothing listens on port 1

	sink, err := NewPubSubSink("test-project", "some-topic", "", nil)
	if err != nil {
		t.Fatalf("NewPubSubSink: %v", err)
	}
	t.Cleanup(func() { _ = sink.Close() })

	// Bounded: gRPC keeps retrying an unreachable address until the call's
	// deadline, which without one is a minute of nothing.
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	err = sink.Ping(ctx)
	if err == nil {
		t.Fatal("Ping succeeded against an address nothing is listening on")
	}
	if strings.Contains(err.Error(), "does not exist") {
		t.Errorf("an unreachable server was reported as a missing topic, which sends "+
			"an operator to the wrong problem: %v", err)
	}
	if !strings.Contains(err.Error(), "failed to check if topic exists") {
		t.Errorf("Ping error does not say what it was doing: %v", err)
	}
}
