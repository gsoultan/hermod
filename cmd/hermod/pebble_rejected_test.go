package main

import (
	"strings"
	"testing"
)

// Pebble must be refused as a metadata store.
//
// It implements 16 of the Storage interface's 100 methods — logs, audit logs,
// traces and the lifecycle calls — and returns "not implemented" for the other
// 84, including everything to do with sources, sinks, workflows and users.
//
// The flag advertised it alongside the real database types and initStorage
// built it regardless, so `--db-type=pebble` produced a server that started
// cleanly and then failed every operation. Worse, computeSetupStatus treats a
// ListUsers error as "configured, with users", so it also reported itself set
// up while being unable to authenticate anyone. The HTTP layer already knew
// better and refused it; the CLI did not.
func TestPebbleIsRefusedAsAMetadataStore(t *testing.T) {
	_, err := initStorage("pebble", t.TempDir())
	if err == nil {
		t.Fatal("pebble was accepted as a metadata store; it cannot hold sources, " +
			"sinks, workflows or users, so the server would start and then fail " +
			"every operation while reporting itself configured")
	}
	msg := err.Error()
	for _, want := range []string{"logging store", "metadata store"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not explain itself: %q lacks %q", msg, want)
		}
	}
	// It must name a way forward, not just say no.
	if !strings.Contains(msg, "sqlite") {
		t.Errorf("the refusal names no usable alternative: %q", msg)
	}
}
