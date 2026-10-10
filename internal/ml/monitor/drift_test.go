package monitor

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/gsoultan/hermod/pkg/ml/worker"
)

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-3 }

func TestPSIIsZeroForTheSameDistributionAndGrowsWithTheShift(t *testing.T) {
	tests := []struct {
		name             string
		expected, actual []float64
		want             float64
	}{
		{"same", []float64{0.5, 0.5}, []float64{0.5, 0.5}, 0},
		{"shifted", []float64{0.5, 0.5}, []float64{0.9, 0.1}, 0.4*math.Log(1.8) - 0.4*math.Log(0.2)},
		// An empty bin is floored rather than dividing by zero or taking log 0.
		{"a bin training never saw", []float64{1, 0}, []float64{0.5, 0.5},
			(0.5-1)*math.Log(0.5/1) + (0.5-psiFloor)*math.Log(0.5/psiFloor)},
	}
	for _, tt := range tests {
		if got := psi(tt.expected, tt.actual); !approx(got, tt.want) {
			t.Errorf("%s: psi = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func f(v float64) *float64 { return &v }

var ageStats = worker.FeatureStats{
	Kind: worker.StatsNumeric, Count: 100, NullFraction: 0.2, Mean: f(15), Std: f(5),
	Edges: []float64{10, 20}, Fractions: []float64{0.25, 0.5, 0.25},
}

var planStats = worker.FeatureStats{
	Kind: worker.StatsCategorical, Count: 100,
	Top: []worker.CategoryShare{{Value: "pro", Fraction: 0.6}, {Value: "3", Fraction: 0.3}}, OtherFraction: 0.1,
}

// Bins match the worker's (ml/worker/hermod_ml/stats.py): v <= edge goes
// in that edge's bin; the last bin holds what is missing.
func TestANumericHistogramBinsLikeTheWorker(t *testing.T) {
	h := newHistogram(ageStats)
	want := []float64{0.2, 0.4, 0.2, 0.2}
	if got := h.expected(); !equalApprox(got, want) {
		t.Errorf("expected = %v, want %v", got, want)
	}
	for _, v := range []any{5.0, 10.0, true, 15, json.Number("12"), "18", 25.0, nil, "abc", math.NaN(), math.Inf(1)} {
		h.add(v)
	}
	// 5, 10, true(1) | 15, 12, 18 | 25 | nil, "abc", NaN, +Inf
	wantCounts := []int64{3, 3, 1, 4}
	for i, c := range wantCounts {
		if h.counts[i] != c {
			t.Errorf("counts = %v, want %v", h.counts, wantCounts)
			break
		}
	}
	if h.total() != 11 {
		t.Errorf("total = %d", h.total())
	}
}

func TestACategoricalHistogramKeepsTopValuesOtherAndMissing(t *testing.T) {
	h := newHistogram(planStats)
	if got, want := h.expected(), []float64{0.6, 0.3, 0.1, 0}; !equalApprox(got, want) {
		t.Errorf("expected = %v, want %v", got, want)
	}
	// 3.0 is "3" to the worker, as to_text renders an integral float.
	for _, v := range []any{"pro", "pro", 3.0, "enterprise", "", nil, false} {
		h.add(v)
	}
	want := []int64{2, 1, 2, 2} // pro x2 | "3" | enterprise, "false" | "", nil
	for i, c := range want {
		if h.counts[i] != c {
			t.Errorf("counts = %v, want %v", h.counts, want)
			break
		}
	}
}

func TestAFeatureTrainedWithNoValuesStillHasABinForValues(t *testing.T) {
	h := newHistogram(worker.FeatureStats{Kind: worker.StatsNumeric, Count: 10, NullFraction: 1})
	if got := h.expected(); !equalApprox(got, []float64{0, 1}) {
		t.Errorf("expected = %v", got)
	}
	h.add(3.0)
	h.add(nil)
	if h.counts[0] != 1 || h.counts[1] != 1 {
		t.Errorf("counts = %v", h.counts)
	}
}

func TestDriftStatusFollowsTheThresholds(t *testing.T) {
	for _, tt := range []struct {
		psi  float64
		want string
	}{{0.05, StatusOK}, {0.1, StatusWarn}, {0.2, StatusWarn}, {0.25, StatusAlert}, {3, StatusAlert}} {
		if got := status(tt.psi, 0.1, 0.25); got != tt.want {
			t.Errorf("status(%v) = %s, want %s", tt.psi, got, tt.want)
		}
	}
}

func equalApprox(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !approx(a[i], b[i]) {
			return false
		}
	}
	return true
}
