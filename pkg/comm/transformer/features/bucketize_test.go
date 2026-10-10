package features

import (
	"reflect"
	"strings"
	"testing"
)

func TestBucketize(t *testing.T) {
	labelled := map[string]any{
		"field":  "age",
		"edges":  []any{0.0, 18.0, 65.0, 120.0},
		"labels": []any{"child", "adult", "senior"},
	}
	tests := []struct {
		name  string
		cfg   map[string]any
		value any
		field string
		want  any
	}{
		{"inside the first bin", labelled, 5.0, "age_bin", "child"},
		{"a lower edge belongs to its bin", labelled, 18.0, "age_bin", "adult"},
		{"the last edge closes the last bin", labelled, 120.0, "age_bin", "senior"},
		{"numeric text is read", labelled, "64.9", "age_bin", "adult"},
		{"no labels writes the bin index", map[string]any{"field": "age", "edges": "0, 10, 20"}, 15.0, "age_bin", 1},
		{"target field", map[string]any{"field": "age", "edges": `[0,10]`, "targetField": "age_group"}, 3.0, "age_group", 0},
		{"below the range is null by default", labelled, -1.0, "age_bin", nil},
		{"above the range is null by default", labelled, 121.0, "age_bin", nil},
		{"clip puts a low value in the first bin", with(labelled, "outOfRange", "clip"), -1.0, "age_bin", "child"},
		{"clip puts a high value in the last bin", with(labelled, "outOfRange", "clip"), 500.0, "age_bin", "senior"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := apply(t, &Bucketize{}, tt.cfg, map[string]any{"age": tt.value})
			if err != nil {
				t.Fatalf("Transform: %v", err)
			}
			got, ok := out.Data()[tt.field]
			if !ok {
				t.Fatalf("%s was not written", tt.field)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("%s = %#v, want %#v", tt.field, got, tt.want)
			}
		})
	}
}

func TestBucketizeOutOfRangeFail(t *testing.T) {
	cfg := map[string]any{"field": "age", "edges": []any{0.0, 10.0}, "outOfRange": "fail"}
	if _, err := apply(t, &Bucketize{}, cfg, map[string]any{"age": 11.0}); err == nil || !strings.Contains(err.Error(), "11") {
		t.Errorf("want an error naming the value, got %v", err)
	}
}

func TestBucketizeMissingValue(t *testing.T) {
	cfg := map[string]any{"field": "age", "edges": []any{0.0, 10.0}}
	if _, err := apply(t, &Bucketize{}, cfg, map[string]any{}); err == nil {
		t.Error("a missing value should fail the record by default")
	}
	out, err := apply(t, &Bucketize{}, with(cfg, "onMissing", "skip"), map[string]any{"age": "n/a"})
	if err != nil {
		t.Fatalf("skip: %v", err)
	}
	if _, ok := out.Data()["age_bin"]; ok {
		t.Error("skip should leave the record unchanged")
	}
}

func TestBucketizeRejectsBadEdges(t *testing.T) {
	tests := []struct {
		name string
		cfg  map[string]any
		want string
	}{
		{"no field", map[string]any{"edges": []any{0.0, 1.0}}, "field"},
		{"one edge", map[string]any{"field": "x", "edges": []any{0.0}}, "two edges"},
		{"not increasing", map[string]any{"field": "x", "edges": []any{0.0, 5.0, 5.0}}, "increasing"},
		{"not a number", map[string]any{"field": "x", "edges": "0, ten"}, "ten"},
		{"label count", map[string]any{"field": "x", "edges": []any{0.0, 1.0, 2.0}, "labels": []any{"a"}}, "labels"},
		{"unknown out-of-range", map[string]any{"field": "x", "edges": []any{0.0, 1.0}, "outOfRange": "wrap"}, "outOfRange"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := (&Bucketize{}).Prepare(tt.cfg)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Prepare error = %v, want one mentioning %q", err, tt.want)
			}
			if _, err := apply(t, &Bucketize{}, tt.cfg, map[string]any{"x": 0.5}); err == nil {
				t.Fatal("Transform accepted a config Prepare refused")
			}
		})
	}
}

// with returns a copy of cfg with one key changed.
func with(cfg map[string]any, key string, v any) map[string]any {
	out := make(map[string]any, len(cfg)+1)
	for k, val := range cfg {
		out[k] = val
	}
	out[key] = v
	return out
}
