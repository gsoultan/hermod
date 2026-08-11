package factory

import "slices"

// twoPhaseCapableSinkTypes are the sink types whose sinks implement
// hermod.TwoPhaseCommit, and so can be members of a transactional group.
//
// A group refuses to start when a member cannot participate, and that refusal
// arrives when the workflow runs. The editor uses this list to keep such a
// member from being chosen in the first place, which turns a runtime failure
// into a choice that was never offered.
//
// It is a list rather than something derived, because deciding whether a sink
// implements an interface means constructing one, and constructing one means
// having its configuration. So it is written down — and twophase_test.go builds
// each entry and checks the interface really is there, in both directions: a
// type listed here that does not implement it fails, and a type that gains the
// implementation without being added fails too. The list cannot drift from the
// code without the build saying so.
//
// postgres and yugabyte share a sink (see the switch in CreateSink), which is
// why both are here: yugabyte speaks the PostgreSQL wire protocol including
// PREPARE TRANSACTION.
var twoPhaseCapableSinkTypes = []string{
	"postgres",
	"yugabyte",
}

// TwoPhaseCapableSinkTypes returns the sink types that can join a transactional
// group. The result is a copy: callers sort and filter it.
func TwoPhaseCapableSinkTypes() []string {
	return slices.Clone(twoPhaseCapableSinkTypes)
}

// SupportsTwoPhaseCommit reports whether a sink of this type can be a member of
// a transactional group.
func SupportsTwoPhaseCommit(sinkType string) bool {
	return slices.Contains(twoPhaseCapableSinkTypes, sinkType)
}
