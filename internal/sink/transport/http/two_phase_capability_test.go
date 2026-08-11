package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/user/hermod/internal/factory"
)

// ---------------------------------------------------------------------------
// Which sinks the editor may offer as transactional group members.
//
// The workflow editor has to filter its member picker to sinks that can take
// part in two-phase commit, and it cannot work that out for itself. The
// alternative — a list of connector capabilities kept by hand in TypeScript — is
// the shape of claim that has gone stale repeatedly in this repository, and the
// cost of it being wrong here is a group the user builds in the editor that
// refuses to start when the workflow runs.
//
// So the backend answers, from the same list the engine enforces.
// ---------------------------------------------------------------------------

func TestTwoPhaseCapabilityMatchesWhatTheEngineEnforces(t *testing.T) {
	h := &SinkHandler{}
	rec := httptest.NewRecorder()
	h.ListTwoPhaseCapableSinkTypes(rec,
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/sinks/capabilities/two-phase", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}

	var got struct {
		Types []string `json:"types"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding: %v", err)
	}

	want := factory.TwoPhaseCapableSinkTypes()
	if len(got.Types) != len(want) {
		t.Fatalf("served %v, engine enforces %v", got.Types, want)
	}
	for _, sinkType := range want {
		if !slices.Contains(got.Types, sinkType) {
			t.Errorf("%q can join a group but is not served to the editor, so nobody can select it", sinkType)
		}
	}
}

// TestTheCapabilityListIsNotEmpty: an empty list disables the member picker
// entirely, which looks like the feature being unavailable rather than broken.
func TestTheCapabilityListIsNotEmpty(t *testing.T) {
	h := &SinkHandler{}
	rec := httptest.NewRecorder()
	h.ListTwoPhaseCapableSinkTypes(rec,
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/sinks/capabilities/two-phase", nil))

	var got struct {
		Types []string `json:"types"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&got)
	if len(got.Types) == 0 {
		t.Error("no sink type is offered, so a transactional group can never be assembled in the editor")
	}
}
