package features

import (
	"math"
	"strings"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/comm/transformer"
)

// apply runs one transformer over a fresh message holding fields.
func apply(t *testing.T, tr transformer.Transformer, cfg, fields map[string]any) (hermod.Message, error) {
	t.Helper()
	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	for k, v := range fields {
		msg.SetData(k, v)
	}
	return tr.Transform(t.Context(), msg, cfg)
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestScaleRegistersUnderItsName(t *testing.T) {
	for _, name := range []string{"scale", "encode", "bucketize", "rolling", "anomaly_score"} {
		if _, ok := transformer.Get(name); !ok {
			t.Errorf("%s is not registered", name)
		}
	}
}

func TestScaleUsesTheFittedStatsFromConfig(t *testing.T) {
	tests := []struct {
		name   string
		cfg    map[string]any
		fields map[string]any
		want   map[string]float64
	}{
		{
			name: "min-max writes to <field>_scaled by default",
			cfg: map[string]any{
				"method": "minmax",
				"fields": []any{map[string]any{"field": "amount", "min": 10.0, "max": 110.0}},
			},
			fields: map[string]any{"amount": 60.0},
			want:   map[string]float64{"amount_scaled": 0.5},
		},
		{
			name: "z-score with a target field, numbers read from text",
			cfg: map[string]any{
				"method": "zscore",
				"fields": `[{"field":"age","mean":40,"std":10,"targetField":"age_z"}]`,
			},
			fields: map[string]any{"age": "55"},
			want:   map[string]float64{"age_z": 1.5},
		},
		{
			name: "a row's method overrides the node's",
			cfg: map[string]any{
				"method": "minmax",
				"fields": []any{
					map[string]any{"field": "a", "min": 0.0, "max": 4.0},
					map[string]any{"field": "b", "method": "zscore", "mean": 1.0, "std": 2.0},
				},
			},
			fields: map[string]any{"a": 1.0, "b": 5.0},
			want:   map[string]float64{"a_scaled": 0.25, "b_scaled": 2},
		},
		{
			name: "pasted stats scale every field they name when no rows are given",
			cfg: map[string]any{
				"method": "zscore",
				"stats":  `{"x":{"mean":2,"std":4},"y":{"mean":-1,"std":0.5}}`,
			},
			fields: map[string]any{"x": 10.0, "y": 0.0},
			want:   map[string]float64{"x_scaled": 2, "y_scaled": 2},
		},
		{
			name: "a row without numbers takes them from the pasted stats",
			cfg: map[string]any{
				"method": "minmax",
				"fields": []any{map[string]any{"field": "x", "targetField": "x01"}},
				"stats":  map[string]any{"x": map[string]any{"min": 0.0, "max": 200.0}, "unused": map[string]any{"min": 0.0, "max": 1.0}},
			},
			fields: map[string]any{"x": 50.0},
			want:   map[string]float64{"x01": 0.25},
		},
		{
			name: "clip keeps min-max output in [0, 1]",
			cfg: map[string]any{
				"method": "minmax",
				"clip":   true,
				"fields": []any{
					map[string]any{"field": "lo", "min": 0.0, "max": 10.0},
					map[string]any{"field": "hi", "min": 0.0, "max": 10.0},
				},
			},
			fields: map[string]any{"lo": -5.0, "hi": 25.0},
			want:   map[string]float64{"lo_scaled": 0, "hi_scaled": 1},
		},
		{
			name: "without clip an unseen value scales past the range, as at training",
			cfg: map[string]any{
				"method": "minmax",
				"fields": []any{map[string]any{"field": "hi", "min": 0.0, "max": 10.0}},
			},
			fields: map[string]any{"hi": 25.0},
			want:   map[string]float64{"hi_scaled": 2.5},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := apply(t, &Scale{}, tt.cfg, tt.fields)
			if err != nil {
				t.Fatalf("Transform: %v", err)
			}
			for field, want := range tt.want {
				got, ok := out.Data()[field].(float64)
				if !ok || !near(got, want) {
					t.Errorf("%s = %v, want %v", field, out.Data()[field], want)
				}
			}
		})
	}
}

// The stats are fitted once and fixed: a second record must not move them.
func TestScaleDoesNotRefitOnTheRecordsItSees(t *testing.T) {
	cfg := map[string]any{
		"method": "zscore",
		"fields": []any{map[string]any{"field": "v", "mean": 0.0, "std": 1.0}},
	}
	s := &Scale{}
	for _, v := range []float64{100, 200, 3} {
		out, err := apply(t, s, cfg, map[string]any{"v": v})
		if err != nil {
			t.Fatalf("Transform: %v", err)
		}
		if got := out.Data()["v_scaled"]; got != v {
			t.Errorf("v=%v scaled to %v, want %v under mean 0, std 1", v, got, v)
		}
	}
}

func TestScaleMissingValue(t *testing.T) {
	base := func(onMissing string) map[string]any {
		return map[string]any{
			"method":    "minmax",
			"onMissing": onMissing,
			"fields":    []any{map[string]any{"field": "v", "min": 0.0, "max": 1.0}},
		}
	}
	if _, err := apply(t, &Scale{}, base(""), map[string]any{"other": 1.0}); err == nil || !strings.Contains(err.Error(), `"v"`) {
		t.Errorf("a missing field should fail the record by default and name the field, got %v", err)
	}
	if _, err := apply(t, &Scale{}, base("fail"), map[string]any{"v": "abc"}); err == nil {
		t.Error("a non-numeric value should fail the record")
	}
	out, err := apply(t, &Scale{}, base("skip"), map[string]any{"other": 1.0})
	if err != nil {
		t.Fatalf("skip: %v", err)
	}
	if _, ok := out.Data()["v_scaled"]; ok {
		t.Error("skip should leave the output unset")
	}
}

func TestScaleRejectsStatsThatCannotScale(t *testing.T) {
	tests := []struct {
		name string
		cfg  map[string]any
		want string
	}{
		{"no fields", map[string]any{"method": "minmax"}, "no fields"},
		{"unknown method", map[string]any{"method": "robust", "fields": []any{map[string]any{"field": "v", "min": 0.0, "max": 1.0}}}, "method"},
		{"max equals min", map[string]any{"method": "minmax", "fields": []any{map[string]any{"field": "v", "min": 1.0, "max": 1.0}}}, "max"},
		{"zero std", map[string]any{"method": "zscore", "fields": []any{map[string]any{"field": "v", "mean": 1.0, "std": 0.0}}}, "std"},
		{"missing mean", map[string]any{"method": "zscore", "fields": []any{map[string]any{"field": "v", "std": 1.0}}}, "mean"},
		{"bad stats JSON", map[string]any{"method": "zscore", "stats": "{nope"}, "stats"},
		{"a row with no field", map[string]any{"method": "minmax", "fields": []any{map[string]any{"min": 0.0, "max": 1.0}}}, "field"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := (&Scale{}).Prepare(tt.cfg)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Prepare error = %v, want one mentioning %q", err, tt.want)
			}
			// The engine ignores a Prepare error, so the record must fail too.
			if _, err := apply(t, &Scale{}, tt.cfg, map[string]any{"v": 1.0}); err == nil {
				t.Fatal("Transform accepted a config Prepare refused")
			}
		})
	}
}

func TestScalePreparedConfigGivesTheSameAnswer(t *testing.T) {
	cfg := map[string]any{
		"method": "zscore",
		"fields": `[{"field":"v","mean":10,"std":5}]`,
	}
	prepared, err := (&Scale{}).Prepare(cfg)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	out, err := apply(t, &Scale{}, prepared, map[string]any{"v": 20.0})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if got := out.Data()["v_scaled"]; got != 2.0 {
		t.Errorf("v_scaled = %v, want 2", got)
	}
}
