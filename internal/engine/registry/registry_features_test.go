package registry

import (
	"encoding/json"
	"fmt"
	"testing"

	_ "github.com/gsoultan/hermod/pkg/comm/transformer/features"
)

// stored decodes a node config the way it comes back from storage: JSON, so
// lists are []any and numbers float64.
func stored(t *testing.T, text string) map[string]any {
	t.Helper()
	var cfg map[string]any
	if err := json.Unmarshal([]byte(text), &cfg); err != nil {
		t.Fatalf("config: %v", err)
	}
	return cfg
}

// The feature-engineering nodes end to end: a stored config through the
// engine's transformation path.
func TestFeatureEngineeringNodesRunFromStoredConfig(t *testing.T) {
	reg := newSimRegistry(t)

	got := transform(t, reg, map[string]any{"amount": 300.0, "country": "SG", "age": 40.0},
		stored(t, `{"transType":"scale","method":"zscore","stats":"{\"amount\":{\"mean\":100,\"std\":50}}"}`))
	if got["amount_scaled"] != 4.0 {
		t.Errorf("scale: amount_scaled = %v, want 4", got["amount_scaled"])
	}

	got = transform(t, reg, map[string]any{"country": "SG"},
		stored(t, `{"transType":"encode","field":"country","method":"onehot","categories":["ID","SG"]}`))
	// Written as ints, read back as float64 once serialised: compare the text.
	if fmt.Sprint(got["country_SG"], got["country_ID"], got["country_other"]) != "1 0 0" {
		t.Errorf("encode: got %v", got)
	}

	got = transform(t, reg, map[string]any{"age": 40.0},
		stored(t, `{"transType":"bucketize","field":"age","edges":[0,18,65],"labels":["minor","adult"]}`))
	if got["age_bin"] != "adult" {
		t.Errorf("bucketize: age_bin = %v, want adult", got["age_bin"])
	}

	rolling := stored(t, `{"transType":"rolling","field":"amount","keyBy":"customer","size":3,"features":["count","mean"]}`)
	transform(t, reg, map[string]any{"customer": "c1", "amount": 10.0}, rolling)
	got = transform(t, reg, map[string]any{"customer": "c1", "amount": 20.0}, rolling)
	if got["amount_count"] != 2.0 || got["amount_mean"] != 15.0 {
		t.Errorf("rolling: count %v, mean %v; want 2 and 15", got["amount_count"], got["amount_mean"])
	}

	anomaly := stored(t, `{"transType":"anomaly_score","field":"amount","size":5,"minEvents":3}`)
	for _, v := range []float64{10, 11, 9} {
		transform(t, reg, map[string]any{"amount": v}, anomaly)
	}
	got = transform(t, reg, map[string]any{"amount": 100.0}, anomaly)
	if got["amount_is_anomaly"] != true {
		t.Errorf("anomaly_score: %v", got)
	}
}
