package factory

import (
	"slices"
	"testing"

	hermod "github.com/gsoultan/Hermod"
)

// ---------------------------------------------------------------------------
// Which sinks can join a transactional group.
//
// A txgroup member must implement hermod.TwoPhaseCommit, and the group refuses
// to start if one does not. That refusal happens when the workflow runs, which
// is the worst moment to find out — so the editor filters the member picker to
// the types that can actually participate.
//
// The filtering is only as good as the list behind it, and a list of connector
// capabilities maintained by hand in the UI is the exact shape of claim that has
// gone stale repeatedly in this repo. So the list lives here, next to the switch
// that builds the sinks, and these tests make it answerable to the code: a type
// that is listed must really implement the interface, and a type that quietly
// gains or loses the implementation fails the build until the list is updated.
// ---------------------------------------------------------------------------

// minimalConfig is enough to construct a sink. None of these dial anything at
// construction; the suite in pkg/comm/conformance covers that separately.
func minimalConfig(sinkType string) SinkConfig {
	return SinkConfig{
		ID:   "t",
		Type: sinkType,
		Config: hermod.StringMap{
			"conn":  "postgres://u:p@127.0.0.1:1/d",
			"table": "t",
			"topic": "t",
			"url":   "http://127.0.0.1:1/",
			"path":  "/tmp/hermod-twophase-test",
			"host":  "127.0.0.1",
			"port":  "1",
		},
	}
}

func TestListedTypesReallySupportTwoPhaseCommit(t *testing.T) {
	listed := TwoPhaseCapableSinkTypes()
	if len(listed) == 0 {
		t.Fatal("no sink type is listed as two-phase capable, so a transactional group can never be built")
	}

	for _, sinkType := range listed {
		snk, err := CreateSinkForTransactionGroup(minimalConfig(sinkType))
		if err != nil {
			t.Errorf("%s is listed as two-phase capable but could not be constructed: %v", sinkType, err)
			continue
		}
		if _, ok := snk.(hermod.TwoPhaseCommit); !ok {
			t.Errorf("%s is offered as a transactional group member but does not implement "+
				"hermod.TwoPhaseCommit; the group would refuse to start when the workflow runs", sinkType)
		}
		_ = snk.Close()
	}
}

// TestUnlistedTypesDoNotSupportTwoPhaseCommit is the half that keeps the list
// honest in the other direction. A sink that gains two-phase support without
// being listed stays invisible in the editor, which is a feature nobody can
// reach.
func TestUnlistedTypesDoNotSupportTwoPhaseCommit(t *testing.T) {
	listed := TwoPhaseCapableSinkTypes()

	// The SQL sinks are the plausible candidates for gaining it, plus a couple of
	// sinks whose delivery model makes two-phase commit impossible.
	for _, sinkType := range []string{
		"mysql", "mariadb", "mssql", "oracle", "clickhouse", "sqlite",
		"kafka", "http", "stdout", "elasticsearch", "mongodb",
	} {
		if slices.Contains(listed, sinkType) {
			continue
		}
		snk, err := CreateSinkForTransactionGroup(minimalConfig(sinkType))
		if err != nil {
			continue // cannot be built from a minimal config; nothing to assert
		}
		if _, ok := snk.(hermod.TwoPhaseCommit); ok {
			t.Errorf("%s implements hermod.TwoPhaseCommit but is not in "+
				"TwoPhaseCapableSinkTypes, so the editor will not offer it as a "+
				"transactional group member; add it to the list", sinkType)
		}
		_ = snk.Close()
	}
}

// TestTheListHasNoDuplicatesOrBlanks: it is served to the UI as-is.
func TestTheListHasNoDuplicatesOrBlanks(t *testing.T) {
	seen := map[string]bool{}
	for _, sinkType := range TwoPhaseCapableSinkTypes() {
		if sinkType == "" {
			t.Error("the list contains an empty sink type")
		}
		if seen[sinkType] {
			t.Errorf("%q is listed twice", sinkType)
		}
		seen[sinkType] = true
	}
}

// TestTheListIsACopy: it is package state, and a caller that sorts or appends to
// what it is handed would corrupt it for everyone else.
func TestTheListIsACopy(t *testing.T) {
	first := TwoPhaseCapableSinkTypes()
	if len(first) == 0 {
		t.Skip("nothing to mutate")
	}
	first[0] = "mutated"

	if slices.Contains(TwoPhaseCapableSinkTypes(), "mutated") {
		t.Error("a caller's mutation changed the list for everyone")
	}
}
