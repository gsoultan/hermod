package secrets

import (
	"context"
	"os"
	"testing"
)

func TestEnvManager(t *testing.T) {
	os.Setenv("HERMOD_TEST_SECRET", "my-secret-value")
	defer os.Unsetenv("HERMOD_TEST_SECRET")

	mgr := &EnvManager{Prefix: "HERMOD_"}
	val, err := mgr.Get(context.Background(), "TEST_SECRET")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "my-secret-value" {
		t.Errorf("expected my-secret-value, got %s", val)
	}

	// Fallback test
	os.Setenv("NO_PREFIX_SECRET", "direct-value")
	defer os.Unsetenv("NO_PREFIX_SECRET")
	val, err = mgr.Get(context.Background(), "NO_PREFIX_SECRET")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "direct-value" {
		t.Errorf("expected direct-value, got %s", val)
	}
}

func TestCombinedManager(t *testing.T) {
	mgr1 := &EnvManager{Prefix: "MGR1_"}
	mgr2 := &EnvManager{Prefix: "MGR2_"}

	os.Setenv("MGR2_KEY", "value2")
	defer os.Unsetenv("MGR2_KEY")

	combined := &CombinedManager{
		Managers: []Manager{mgr1, mgr2},
	}

	val, err := combined.Get(context.Background(), "KEY")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "value2" {
		t.Errorf("expected value2, got %s", val)
	}
}

func TestResolveSecret(t *testing.T) {
	os.Setenv("SECRET_KEY", "resolved-value")
	defer os.Unsetenv("SECRET_KEY")

	mgr := &EnvManager{}

	// Case 1: prefixed with secret:
	val := ResolveSecret(context.Background(), mgr, "secret:SECRET_KEY")
	if val != "resolved-value" {
		t.Errorf("expected resolved-value, got %s", val)
	}

	// Case 2: not prefixed
	val = ResolveSecret(context.Background(), mgr, "plain-value")
	if val != "plain-value" {
		t.Errorf("expected plain-value, got %s", val)
	}

	// Case 3: prefixed but not found
	val = ResolveSecret(context.Background(), mgr, "secret:NON_EXISTENT")
	if val != "secret:NON_EXISTENT" {
		t.Errorf("expected secret:NON_EXISTENT, got %s", val)
	}
}
