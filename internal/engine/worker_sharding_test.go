package engine

import "testing"

func TestWorkerIsAssigned_StabilityAndDistribution(t *testing.T) {
	w := &Worker{}
	w.SetWorkerConfig(0, 3, "", "")

	ids := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}
	counts := make([]int, 3)

	for _, id := range ids {
		for shard := 0; shard < 3; shard++ {
			w.workerID = shard
			if w.isAssigned(id) {
				counts[shard]++
			}
		}
	}

	// All workflows should be assigned to exactly one shard
	total := counts[0] + counts[1] + counts[2]
	if total != len(ids) {
		t.Fatalf("expected total assignments %d, got %d (counts=%v)", len(ids), total, counts)
	}

	// Basic distribution sanity: no shard should be empty for this sample set
	for i, c := range counts {
		if c == 0 {
			t.Fatalf("expected non-empty assignment for shard %d, got 0", i)
		}
	}
}
