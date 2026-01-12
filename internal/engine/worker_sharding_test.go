package engine

import (
	"testing"
)

func TestWorker_isAssigned(t *testing.T) {
	w := &Worker{
		workerID:     0,
		totalWorkers: 2,
	}

	// Test case 1: Even hash
	// "a" -> 97, "b" -> 98. "ab" -> 195. 195 % 2 = 1.
	// "a" -> 97. 97 % 2 = 1.
	// "b" -> 98. 98 % 2 = 0.

	if !w.isAssigned("b") {
		t.Errorf("Expected 'b' to be assigned to worker 0")
	}
	if w.isAssigned("a") {
		t.Errorf("Expected 'a' NOT to be assigned to worker 0")
	}

	w.workerID = 1
	if w.isAssigned("b") {
		t.Errorf("Expected 'b' NOT to be assigned to worker 1")
	}
	if !w.isAssigned("a") {
		t.Errorf("Expected 'a' to be assigned to worker 1")
	}

	// Test case 2: Single worker
	w.totalWorkers = 1
	w.workerID = 0
	if !w.isAssigned("any") {
		t.Errorf("Expected everything to be assigned to single worker")
	}
}
