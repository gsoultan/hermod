package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/pkg/ml/recommend"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// basketRows are two tastes: the first half of the people buy from the a
// items, the rest from the b items, perTaste of each; everyone buys five,
// a day apart.
func basketRows(people, perTaste int) []map[string]any {
	rng := rand.New(rand.NewPCG(5, 6))
	var rows []map[string]any
	for u := range people {
		group := "a"
		if u >= people/2 {
			group = "b"
		}
		for n, i := range rng.Perm(perTaste)[:5] {
			rows = append(rows, map[string]any{
				"customer": fmt.Sprintf("u%d", u), "product": fmt.Sprintf("%s%d", group, i),
				"qty": float64(1 + rng.IntN(3)), "at": fmt.Sprintf("2026-01-%02dT10:00:00Z", n+1),
			})
		}
	}
	return rows
}

func recommendSpec(algorithm string) worker.TrainSpec {
	return worker.TrainSpec{Dataset: "baskets", Task: worker.TaskRecommendation, Algorithm: algorithm, Seed: seed(7),
		Recommend: &worker.RecommendSpec{User: "customer", Item: "product", Strength: "qty", Time: "at"}}
}

func TestRecommendersLearnTheTwoTastes(t *testing.T) {
	for _, alg := range []string{"als", "item_similarity"} {
		t.Run(alg, func(t *testing.T) {
			_, c := newEngine(t)
			ctx := context.Background()
			// More items than ALS has factors, so it has to generalise.
			appendRows(t, c, "baskets", basketRows(400, 40))
			v, err := c.Train(ctx, "v", "rec", recommendSpec(alg))
			if err != nil {
				t.Fatalf("Train: %v", err)
			}
			if v.Task != worker.TaskRecommendation || v.Algorithm != alg || v.Scenario != "products" {
				t.Errorf("version = %s %s %s", v.Task, v.Algorithm, v.Scenario)
			}
			if v.Catalog == nil || v.Catalog.Users != 400 || v.Catalog.Items != 80 {
				t.Errorf("catalog = %+v", v.Catalog)
			}
			// Each person holds back ceil(0.2 * 5) = 1, their latest.
			if v.Rows.Train != 1600 || v.Rows.Test != 400 {
				t.Errorf("rows = %+v, want 1600/400", v.Rows)
			}
			for _, k := range []string{"hit_rate@10", "precision@10", "recall@10", "ndcg@10", "coverage", "baseline_ndcg@10", "score"} {
				if _, ok := v.Metrics[k]; !ok {
					t.Errorf("metric %q missing from %v", k, v.Metrics)
				}
			}
			if v.Metrics["score"] != v.Metrics["ndcg@10"] || v.Metrics["hit_rate@10"] <= v.Metrics["baseline_hit_rate@10"] ||
				v.Metrics["ndcg@10"] <= v.Metrics["baseline_ndcg@10"] {
				t.Errorf("metrics do not beat the popular baseline: %v", v.Metrics)
			}
			var samples []recSample
			if err := json.Unmarshal(v.Samples, &samples); err != nil || len(samples) != recommendSamples {
				t.Fatalf("samples = %s, %v", v.Samples, err)
			}
			raw, err := c.RecommenderFile(ctx, "v", "rec", v.Version)
			if err != nil {
				t.Fatalf("RecommenderFile: %v", err)
			}
			m, err := recommend.Parse(raw)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if m.Algorithm() != alg || m.Users() != 400 || m.Items() != 80 {
				t.Errorf("artifact = %s %d %d", m.Algorithm(), m.Users(), m.Items())
			}
			got, known := m.ForUser("u3", 5, true)
			if !known || len(got) != 5 {
				t.Fatalf("ForUser = %v, %v", got, known)
			}
			for _, it := range got {
				if !strings.HasPrefix(it.Item, "a") {
					t.Errorf("u3 (a taste) was recommended %v", got)
				}
			}
			similar, known := m.Similar("b2", 5)
			if !known || len(similar) == 0 {
				t.Fatalf("Similar = %v, %v", similar, known)
			}
			if alg == "als" {
				similar = similar[:1] // factor cosine is noisier than co-occurrence
			}
			for _, it := range similar {
				if !strings.HasPrefix(it.Item, "b") || it.Item == "b2" {
					t.Errorf("b2 is similar to %v", similar)
				}
			}
		})
	}
}

func TestRecommenderAutoScoresBothCandidates(t *testing.T) {
	_, c := newEngine(t)
	appendRows(t, c, "baskets", basketRows(60, 8))
	v, err := c.Train(context.Background(), "v", "rec", recommendSpec("auto"))
	if err != nil {
		t.Fatalf("Train: %v", err)
	}
	if len(v.Candidates) != 2 {
		t.Fatalf("candidates = %v", v.Candidates)
	}
	want := "item_similarity"
	if v.Candidates["als"] > v.Candidates["item_similarity"] {
		want = "als"
	}
	if v.Algorithm != want {
		t.Errorf("algorithm = %s with candidates %v, want %s", v.Algorithm, v.Candidates, want)
	}
}

func TestRecommenderRepeatsWithTheSeed(t *testing.T) {
	var files [][]byte
	for range 2 {
		_, c := newEngine(t)
		ctx := context.Background()
		appendRows(t, c, "baskets", basketRows(60, 8))
		spec := recommendSpec("als")
		spec.Recommend.Time = "" // a random holdout, drawn with the seed
		v, err := c.Train(ctx, "v", "rec", spec)
		if err != nil {
			t.Fatalf("Train: %v", err)
		}
		raw, err := c.RecommenderFile(ctx, "v", "rec", v.Version)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, raw)
	}
	if !bytes.Equal(files[0], files[1]) {
		t.Error("two trainings with the same seed gave different artifacts")
	}
}

func TestRecommenderRefusals(t *testing.T) {
	_, c := newEngine(t)
	ctx := context.Background()
	appendRows(t, c, "baskets", basketRows(60, 8)[:6])
	if _, err := c.Train(ctx, "v", "rec", recommendSpec("als")); err == nil || !strings.Contains(err.Error(), "at least 10 usable interactions") {
		t.Errorf("too few interactions: %v", err)
	}
	var once []map[string]any
	for i := range 10 {
		once = append(once, map[string]any{"customer": fmt.Sprintf("c%d", i), "product": fmt.Sprintf("p%d", i%2), "when": "soon"})
	}
	appendRows(t, c, "once", once)
	spec := worker.TrainSpec{Dataset: "once", Task: worker.TaskRecommendation, Algorithm: "item_similarity",
		Recommend: &worker.RecommendSpec{User: "customer", Item: "product"}}
	if _, err := c.Train(ctx, "v", "rec", spec); err == nil || !strings.Contains(err.Error(), "nothing to hold back") {
		t.Errorf("no repeat customers: %v", err)
	}
	spec.Recommend.Time = "when"
	if _, err := c.Train(ctx, "v", "rec", spec); err == nil || !strings.Contains(err.Error(), "no dates") {
		t.Errorf("unreadable time column: %v", err)
	}
}

func TestRecommenderStopsWhenCancelled(t *testing.T) {
	_, c := newEngine(t)
	appendRows(t, c, "baskets", basketRows(60, 8))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Train(ctx, "v", "rec", recommendSpec("als")); err == nil {
		t.Error("a cancelled training succeeded")
	}
}

func TestCholeskySolve(t *testing.T) {
	// [[4 2] [2 3]] x = [2 1]  =>  x = [0.5 0]
	a, b := []float64{4, 2, 2, 3}, []float64{2, 1}
	choleskySolve(a, b, 2)
	if math.Abs(b[0]-0.5) > 1e-12 || math.Abs(b[1]) > 1e-12 {
		t.Errorf("x = %v, want [0.5 0]", b)
	}
}

func TestHoldoutKeepsTheLatest(t *testing.T) {
	in := &interactions{user: []int{0, 0, 0, 1}, item: []int{0, 1, 2, 0}, strength: []float64{1, 1, 1, 1}, time: []float64{3, 1, 2, 9}}
	train, test := in.holdout([]int{0, 1, 2, 3}, 0.2, 1)
	if fmt.Sprint(train, test) != "[1 2 3] [0]" {
		t.Errorf("holdout = %v %v, want [1 2 3] [0]", train, test)
	}
}
