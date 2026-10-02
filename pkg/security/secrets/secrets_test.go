package secrets

import (
	"testing"
)

// An env secret is read from the prefixed variable only. It used to fall back
// to the bare name, so `secret:HERMOD_JWT_SECRET` in a connector config -- and
// env('HERMOD_JWT_SECRET') in any expression -- handed whoever could edit a
// workflow the process environment, and the Security tab's "only env vars
// starting with this prefix will be searched" was not true.
func TestEnvManagerReadsOnlyThePrefixedVariable(t *testing.T) {
	t.Setenv("HERMOD_TEST_SECRET", "prefixed-value")
	t.Setenv("HERMOD_SECRET_API_KEY", "api-key")
	t.Setenv("HERMOD_SECRETS_TEST_BARE", "bare-value")

	tests := []struct {
		name   string
		prefix string
		key    string
		want   string
	}{
		{"a key under the prefix", "HERMOD_", "TEST_SECRET", "prefixed-value"},
		{"a key that already carries the prefix", "HERMOD_SECRET_", "HERMOD_SECRET_API_KEY", "api-key"},
		{"no prefix configured reads HERMOD_SECRET_", "", "API_KEY", "api-key"},
		{"a variable outside the prefix", "HERMOD_SECRET_", "HERMOD_SECRETS_TEST_BARE", ""},
		{"a variable outside the default prefix", "", "HERMOD_SECRETS_TEST_BARE", ""},
		{"a variable that does not exist", "HERMOD_SECRET_", "MISSING", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := (&EnvManager{Prefix: tc.prefix}).Get(t.Context(), tc.key)
			if err != nil {
				t.Fatalf("Get(%q): %v", tc.key, err)
			}
			if got != tc.want {
				t.Errorf("Get(%q) with prefix %q = %q, want %q", tc.key, tc.prefix, got, tc.want)
			}
		})
	}
}

// The bare lookup stays available for one release behind an operator's own
// environment variable, so a deployment can rename its variables without an
// outage. Nothing a workflow editor can write reaches that switch.
func TestEnvManagerReadsABareVariableOnlyWhenTheOperatorAllowsIt(t *testing.T) {
	t.Setenv("HERMOD_SECRETS_TEST_BARE", "bare-value")
	t.Setenv("HERMOD_SECRET_BOTH", "prefixed")
	t.Setenv("BOTH", "bare")
	mgr := &EnvManager{Prefix: "HERMOD_SECRET_"}

	tests := []struct {
		name  string
		allow string
		key   string
		want  string
	}{
		{"allowed", "1", "HERMOD_SECRETS_TEST_BARE", "bare-value"},
		{"allowed, spelled true", "true", "HERMOD_SECRETS_TEST_BARE", "bare-value"},
		{"switched off", "0", "HERMOD_SECRETS_TEST_BARE", ""},
		{"not a boolean", "yes please", "HERMOD_SECRETS_TEST_BARE", ""},
		{"the prefixed variable still wins", "1", "BOTH", "prefixed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(AllowUnprefixedEnv, tc.allow)
			got, err := mgr.Get(t.Context(), tc.key)
			if err != nil {
				t.Fatalf("Get(%q): %v", tc.key, err)
			}
			if got != tc.want {
				t.Errorf("Get(%q) with %s=%q = %q, want %q", tc.key, AllowUnprefixedEnv, tc.allow, got, tc.want)
			}
		})
	}
}

func TestCombinedManager(t *testing.T) {
	mgr1 := &EnvManager{Prefix: "MGR1_"}
	mgr2 := &EnvManager{Prefix: "MGR2_"}

	t.Setenv("MGR2_KEY", "value2")

	combined := &CombinedManager{
		Managers: []Manager{mgr1, mgr2},
	}

	val, err := combined.Get(t.Context(), "KEY")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "value2" {
		t.Errorf("expected value2, got %s", val)
	}
}

func TestResolveSecret(t *testing.T) {
	t.Setenv("HERMOD_SECRET_SECRET_KEY", "resolved-value")
	t.Setenv("HERMOD_SECRETS_TEST_BARE", "bare-value")

	mgr := &EnvManager{}

	tests := []struct {
		name  string
		value string
		want  string
	}{
		{"a secret: reference", "secret:SECRET_KEY", "resolved-value"},
		{"a braced reference", "{{secret:SECRET_KEY}}", "resolved-value"},
		{"a plain value", "plain-value", "plain-value"},
		{"a reference that is not found", "secret:NON_EXISTENT", "secret:NON_EXISTENT"},
		// A connector config naming a variable outside the prefix gets its own
		// text back, not the variable.
		{"a reference outside the prefix", "secret:HERMOD_SECRETS_TEST_BARE", "secret:HERMOD_SECRETS_TEST_BARE"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveSecret(t.Context(), mgr, tc.value); got != tc.want {
				t.Errorf("ResolveSecret(%q) = %q, want %q", tc.value, got, tc.want)
			}
		})
	}
}
