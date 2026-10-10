package features

import (
	"hash/fnv"
	"reflect"
	"strings"
	"testing"
)

func TestEncodeOneHot(t *testing.T) {
	tests := []struct {
		name   string
		cfg    map[string]any
		fields map[string]any
		want   map[string]any
		absent []string
	}{
		{
			name:   "a listed category sets its column and clears the rest",
			cfg:    map[string]any{"field": "country", "method": "onehot", "categories": []any{"ID", "SG", "MY"}},
			fields: map[string]any{"country": "SG"},
			want:   map[string]any{"country_ID": 0, "country_SG": 1, "country_MY": 0, "country_other": 0},
		},
		{
			name:   "an unlisted category goes to the other bucket",
			cfg:    map[string]any{"field": "country", "method": "onehot", "categories": `["ID","SG"]`},
			fields: map[string]any{"country": "TH"},
			want:   map[string]any{"country_ID": 0, "country_SG": 0, "country_other": 1},
		},
		{
			name:   "comma-separated categories and a custom prefix",
			cfg:    map[string]any{"field": "plan", "method": "onehot", "categories": "free, pro", "prefix": "is_"},
			fields: map[string]any{"plan": "pro"},
			want:   map[string]any{"is_free": 0, "is_pro": 1, "is_other": 0},
		},
		{
			name:   "otherBucket off writes all zeros for an unknown value",
			cfg:    map[string]any{"field": "plan", "method": "onehot", "categories": []any{"free", "pro"}, "otherBucket": false},
			fields: map[string]any{"plan": "team"},
			want:   map[string]any{"plan_free": 0, "plan_pro": 0},
			absent: []string{"plan_other"},
		},
		{
			name:   "one-hot is the default method",
			cfg:    map[string]any{"field": "plan", "categories": []any{"free"}},
			fields: map[string]any{"plan": "free"},
			want:   map[string]any{"plan_free": 1, "plan_other": 0},
		},
		{
			name:   "numbers match their text form",
			cfg:    map[string]any{"field": "tier", "method": "onehot", "categories": []any{1.0, 2.0}},
			fields: map[string]any{"tier": 2.0},
			want:   map[string]any{"tier_1": 0, "tier_2": 1, "tier_other": 0},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := apply(t, &Encode{}, tt.cfg, tt.fields)
			if err != nil {
				t.Fatalf("Transform: %v", err)
			}
			for k, want := range tt.want {
				if got := out.Data()[k]; !reflect.DeepEqual(got, want) {
					t.Errorf("%s = %#v, want %#v", k, got, want)
				}
			}
			for _, k := range tt.absent {
				if _, ok := out.Data()[k]; ok {
					t.Errorf("%s was written", k)
				}
			}
		})
	}
}

func TestEncodeLabel(t *testing.T) {
	cfg := map[string]any{"field": "size", "method": "label", "mapping": `{"S":0,"M":1,"L":2}`}
	out, err := apply(t, &Encode{}, cfg, map[string]any{"size": "L"})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if got := out.Data()["size_label"]; got != 2 {
		t.Errorf("size_label = %#v, want 2", got)
	}

	// An unknown value gets -1 by default, or the configured value.
	out, err = apply(t, &Encode{}, cfg, map[string]any{"size": "XL"})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if got := out.Data()["size_label"]; got != -1 {
		t.Errorf("unknown size_label = %#v, want -1", got)
	}
	withUnknown := map[string]any{"field": "size", "method": "label", "mapping": map[string]any{"S": 0.0}, "unknownValue": 99.0, "targetField": "sz"}
	out, err = apply(t, &Encode{}, withUnknown, map[string]any{"size": "XL"})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if got := out.Data()["sz"]; got != 99 {
		t.Errorf("sz = %#v, want the configured unknownValue 99", got)
	}

	failing := map[string]any{"field": "size", "method": "label", "mapping": map[string]any{"S": 0.0}, "onUnknown": "fail"}
	if _, err := apply(t, &Encode{}, failing, map[string]any{"size": "XL"}); err == nil || !strings.Contains(err.Error(), "XL") {
		t.Errorf("onUnknown fail should name the value, got %v", err)
	}
}

func TestEncodeHashIsStableFNV(t *testing.T) {
	cfg := map[string]any{"field": "user", "method": "hash", "buckets": 16.0}
	h := fnv.New32a()
	_, _ = h.Write([]byte("alice@example.com"))
	want := int(h.Sum32() % 16)

	for range 3 {
		out, err := apply(t, &Encode{}, cfg, map[string]any{"user": "alice@example.com"})
		if err != nil {
			t.Fatalf("Transform: %v", err)
		}
		if got := out.Data()["user_bucket"]; got != want {
			t.Fatalf("user_bucket = %#v, want %d (FNV-1a 32 mod 16)", got, want)
		}
	}
}

func TestEncodeMissingValue(t *testing.T) {
	cfg := map[string]any{"field": "c", "method": "onehot", "categories": []any{"a"}}
	if _, err := apply(t, &Encode{}, cfg, map[string]any{}); err == nil {
		t.Error("a missing field should fail the record by default")
	}
	cfg["onMissing"] = "skip"
	out, err := apply(t, &Encode{}, cfg, map[string]any{})
	if err != nil {
		t.Fatalf("skip: %v", err)
	}
	if _, ok := out.Data()["c_a"]; ok {
		t.Error("skip should leave the record unchanged")
	}
}

func TestEncodeRejectsBadConfig(t *testing.T) {
	tests := []struct {
		name string
		cfg  map[string]any
		want string
	}{
		{"no field", map[string]any{"method": "hash", "buckets": 4.0}, "field"},
		{"unknown method", map[string]any{"field": "c", "method": "target"}, "method"},
		{"onehot without categories", map[string]any{"field": "c", "method": "onehot"}, "categories"},
		{"duplicate category", map[string]any{"field": "c", "method": "onehot", "categories": []any{"a", "a"}}, "twice"},
		{"category clashes with other", map[string]any{"field": "c", "method": "onehot", "categories": []any{"other"}}, "other"},
		{"label without mapping", map[string]any{"field": "c", "method": "label"}, "mapping"},
		{"label to a fraction", map[string]any{"field": "c", "method": "label", "mapping": map[string]any{"a": 0.5}}, "whole number"},
		{"zero buckets", map[string]any{"field": "c", "method": "hash", "buckets": 0.0}, "buckets"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := (&Encode{}).Prepare(tt.cfg)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Prepare error = %v, want one mentioning %q", err, tt.want)
			}
			if _, err := apply(t, &Encode{}, tt.cfg, map[string]any{"c": "a"}); err == nil {
				t.Fatal("Transform accepted a config Prepare refused")
			}
		})
	}
}
