package recommend

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// FuzzParse feeds Parse arbitrary bytes, seeded with valid artifacts. Parse
// must refuse what it cannot read with ErrInvalid and never panic, and a model
// it accepts must answer any call within bounds: at most k items, each an item
// of the model, with a finite score.
func FuzzParse(f *testing.F) {
	f.Add(factorsArtifact().encode(f))
	f.Add(neighborsArtifact().encode(f))
	for _, algorithm := range []string{AlgorithmALS, AlgorithmItemSimilarity} {
		raw, err := os.ReadFile(filepath.Join("testdata", algorithm, "model.rec"))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(raw)
	}
	f.Add([]byte("HREC\x01\x00\x00\x00\x02\x00\x00\x00{}"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		m, err := Parse(raw)
		if err != nil {
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("Parse error %v is not ErrInvalid", err)
			}
			return
		}
		ids := append([]string{"", "nobody"}, m.users...)
		items := append([]string{"", "nothing"}, m.items...)
		for _, k := range []int{-1, 0, 1, 3, MaxK + 5} {
			for _, u := range ids[:min(len(ids), 6)] {
				for _, exclude := range []bool{true, false} {
					got, _ := m.ForUser(u, k, exclude)
					checkItems(t, m, got, k)
				}
			}
			for _, it := range items[:min(len(items), 6)] {
				got, _ := m.Similar(it, k)
				checkItems(t, m, got, k)
			}
		}
	})
}

func checkItems(t *testing.T, m *Model, got []Item, k int) {
	t.Helper()
	if len(got) > ClampK(k) {
		t.Fatalf("%d items for k=%d", len(got), k)
	}
	seen := map[string]bool{}
	for _, it := range got {
		if _, ok := m.itemIndex[it.Item]; !ok {
			t.Fatalf("recommended %q, which is not an item of the model", it.Item)
		}
		if seen[it.Item] {
			t.Fatalf("recommended %q twice", it.Item)
		}
		seen[it.Item] = true
		if it.Score != it.Score {
			t.Fatalf("item %q has a NaN score", it.Item)
		}
	}
}
