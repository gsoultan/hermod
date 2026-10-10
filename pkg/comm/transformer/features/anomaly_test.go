package features

import (
	"math"
	"strings"
	"testing"
)

func TestAnomalyZScoreAgainstTheWindowBeforeTheRecord(t *testing.T) {
	a := &AnomalyScore{}
	cfg := map[string]any{"field": "amount", "keyBy": "card", "method": "zscore", "size": 5.0, "minEvents": 5.0}

	for _, v := range []float64{10, 10, 10, 12, 8} {
		event(t, a, "wf", "n1", nil, cfg, map[string]any{"card": "c1", "amount": v})
	}
	// mean 10, population std sqrt(1.6): 15 is 3.95 deviations out.
	got := event(t, a, "wf", "n1", nil, cfg, map[string]any{"card": "c1", "amount": 15.0})
	wantFloat(t, got, "amount_anomaly_score", 5/math.Sqrt(1.6))
	if got["amount_is_anomaly"] != true {
		t.Errorf("amount_is_anomaly = %v, want true above the default threshold 3", got["amount_is_anomaly"])
	}

	// Another card has its own history, too short to score yet.
	fresh := event(t, a, "wf", "n1", nil, cfg, map[string]any{"card": "c2", "amount": 1e6})
	if fresh["amount_anomaly_score"] != nil || fresh["amount_is_anomaly"] != false {
		t.Errorf("before minEvents: score %v, flag %v; want null and false", fresh["amount_anomaly_score"], fresh["amount_is_anomaly"])
	}
}

func TestAnomalyNormalValueIsNotFlagged(t *testing.T) {
	a := &AnomalyScore{}
	cfg := map[string]any{"field": "v", "size": 5.0, "minEvents": 5.0, "scoreField": "s", "flagField": "f", "threshold": 1.0}
	for _, v := range []float64{10, 10, 10, 12, 8} {
		event(t, a, "wf", "n1", nil, cfg, map[string]any{"v": v})
	}
	got := event(t, a, "wf", "n1", nil, cfg, map[string]any{"v": 11.0})
	wantFloat(t, got, "s", 1/math.Sqrt(1.6))
	if got["f"] != false {
		t.Errorf("f = %v, want false below threshold 1", got["f"])
	}
	// The record joined the window: [10, 10, 12, 8, 11].
	got = event(t, a, "wf", "n1", nil, cfg, map[string]any{"v": 10.2})
	mean := (10 + 10 + 12 + 8 + 11) / 5.0
	sq := func(x float64) float64 { return x * x }
	variance := (sq(10-mean)*2 + sq(12-mean) + sq(8-mean) + sq(11-mean)) / 5
	wantFloat(t, got, "s", math.Abs(10.2-mean)/math.Sqrt(variance))
}

func TestAnomalyIQR(t *testing.T) {
	a := &AnomalyScore{}
	cfg := map[string]any{"field": "v", "method": "iqr", "size": 8.0, "minEvents": 8.0}
	for v := 1.0; v <= 8; v++ {
		event(t, a, "wf", "n1", nil, cfg, map[string]any{"v": v})
	}
	// Q1 2.75, Q3 6.25 (linear interpolation), IQR 3.5.
	got := event(t, a, "wf", "n1", nil, cfg, map[string]any{"v": 20.0})
	wantFloat(t, got, "v_anomaly_score", (20-6.25)/3.5)
	if got["v_is_anomaly"] != true {
		t.Errorf("v_is_anomaly = %v, want true beyond 1.5 IQR", got["v_is_anomaly"])
	}

	b := &AnomalyScore{}
	for v := 1.0; v <= 8; v++ {
		event(t, b, "wf", "n1", nil, cfg, map[string]any{"v": v})
	}
	got = event(t, b, "wf", "n1", nil, cfg, map[string]any{"v": 5.0})
	wantFloat(t, got, "v_anomaly_score", 0)
	if got["v_is_anomaly"] != false {
		t.Errorf("a value between the quartiles is flagged")
	}
	got = event(t, b, "wf", "n1", nil, cfg, map[string]any{"v": 1.0})
	// The window is [2..8, 5]: Q1 3.75, Q3 6.25, IQR 2.5; 1 is 2.75 below Q1.
	wantFloat(t, got, "v_anomaly_score", 2.75/2.5)
}

// A constant history has no spread: the same value scores 0, any other is
// flagged with no finite score.
func TestAnomalyConstantHistory(t *testing.T) {
	a := &AnomalyScore{}
	cfg := map[string]any{"field": "v", "size": 10.0, "minEvents": 3.0}
	for range 3 {
		event(t, a, "wf", "n1", nil, cfg, map[string]any{"v": 5.0})
	}
	got := event(t, a, "wf", "n1", nil, cfg, map[string]any{"v": 5.0})
	wantFloat(t, got, "v_anomaly_score", 0)
	if got["v_is_anomaly"] != false {
		t.Error("the same value is flagged against a constant history")
	}
	got = event(t, a, "wf", "n1", nil, cfg, map[string]any{"v": 6.0})
	if _, ok := got["v_anomaly_score"]; !ok || got["v_anomaly_score"] != nil {
		t.Errorf("score = %#v, want null: there is no finite score", got["v_anomaly_score"])
	}
	if got["v_is_anomaly"] != true {
		t.Error("a new value against a constant history is not flagged")
	}
}

func TestAnomalyPersistentStateSurvivesRestart(t *testing.T) {
	store := newMemStore()
	cfg := map[string]any{"field": "v", "size": 5.0, "minEvents": 3.0, "persistent": true}
	for _, v := range []float64{1, 2, 3} {
		event(t, &AnomalyScore{}, "wf", "n1", store, cfg, map[string]any{"v": v})
	}
	got := event(t, &AnomalyScore{}, "wf", "n1", store, cfg, map[string]any{"v": 2.0})
	wantFloat(t, got, "v_anomaly_score", 0)

	// A rolling node with the same settings keeps its own state.
	r := event(t, &Rolling{}, "wf", "n1", store, map[string]any{"field": "v", "size": 5.0, "persistent": true, "features": []any{"count"}}, map[string]any{"v": 1.0})
	wantFloat(t, r, "v_count", 1)
}

func TestAnomalyRejectsBadConfig(t *testing.T) {
	tests := []struct {
		name string
		cfg  map[string]any
		want string
	}{
		{"no field", map[string]any{"size": 5.0}, "field"},
		{"unknown method", map[string]any{"field": "v", "size": 5.0, "method": "mad"}, "method"},
		{"zero threshold", map[string]any{"field": "v", "size": 5.0, "threshold": 0.0}, "threshold"},
		{"minEvents below two", map[string]any{"field": "v", "size": 5.0, "minEvents": 1.0}, "minEvents"},
		{"no window", map[string]any{"field": "v"}, "size"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := (&AnomalyScore{}).Prepare(tt.cfg)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Prepare error = %v, want one mentioning %q", err, tt.want)
			}
			if _, err := eventErr(t, &AnomalyScore{}, "wf", "n1", nil, tt.cfg, map[string]any{"v": 1.0}); err == nil {
				t.Fatal("Transform accepted a config Prepare refused")
			}
		})
	}
}
