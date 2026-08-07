package lookup

import (
	"testing"
)

// db_lookup returned (msg, nil) — success — on every way it could fail to
// enrich: a key path that resolved to nil, a missing config, a row that was not
// found. The message went on to the sink without the field and without an
// error, so a partially-enriched record was indistinguishable from a fully
// enriched one.
//
// Observed in the CDC → db_lookup → Postgres run: 1 of 12,000 rows arrived with
// no customer_name during a restart, and earlier 19,505 of 20,015 did, both
// with a completely clean log.
//
// onMiss makes the choice explicit and auditable:
//
//	passthrough — emit as-is (previous behaviour, now opt-in)
//	default     — write defaultValue
//	fail        — return an error so the message retries and lands in the DLQ
func TestResolveMissPolicy(t *testing.T) {
	cases := []struct {
		name     string
		config   map[string]any
		hasDeflt bool
		want     missPolicy
	}{
		{"unset without default is passthrough", map[string]any{}, false, missPassthrough},
		{"unset with a default uses it", map[string]any{}, true, missDefault},
		{"explicit passthrough", map[string]any{"onMiss": "passthrough"}, true, missPassthrough},
		{"explicit default", map[string]any{"onMiss": "default"}, true, missDefault},
		{"explicit fail", map[string]any{"onMiss": "fail"}, false, missFail},
		{"case and space tolerant", map[string]any{"onMiss": "  FAIL "}, false, missFail},
		{"unknown value falls back rather than guessing", map[string]any{"onMiss": "explode"}, false, missPassthrough},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveMissPolicy(tc.config, tc.hasDeflt); got != tc.want {
				t.Errorf("resolveMissPolicy(%v, hasDefault=%v) = %v, want %v", tc.config, tc.hasDeflt, got, tc.want)
			}
		})
	}
}

// A miss must be reportable, not merely survivable: an operator needs to know
// enrichment is silently degrading before the data warehouse does.
func TestMissPolicyFailProducesAnError(t *testing.T) {
	err := missError("customers", "$.customer_code", nil)
	if err == nil {
		t.Fatal("onMiss=fail produced no error")
	}
	for _, want := range []string{"customers", "customer_code"} {
		if !contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q, so the operator cannot tell which lookup failed", err, want)
		}
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle || len(needle) == 0 ||
		func() bool {
			for i := 0; i+len(needle) <= len(haystack); i++ {
				if haystack[i:i+len(needle)] == needle {
					return true
				}
			}
			return false
		}())
}
