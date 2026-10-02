package registry

import (
	"context"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
)

type mockSecretManager struct {
	resolved bool
}

func (m *mockSecretManager) Get(ctx context.Context, key string) (string, error) {
	m.resolved = true
	return "resolved-value", nil
}

func TestSecretResolutionInGetOrOpenDB(t *testing.T) {
	sm := &mockSecretManager{}
	reg := NewRegistry(&mockStorage{})
	reg.SetSecretManager(sm)

	src := storage.Source{
		ID:   "test-source",
		Type: "sqlite",
		Config: map[string]string{
			"path":     ":memory:",
			"password": "{{secret:DB_PASS}}",
		},
	}

	// We don't want to actually open a DB connection if possible,
	// but GetOrOpenDB will try to.
	// Since we use sqlite :memory:, it should be fine.

	_, err := reg.GetOrOpenDB(src)
	if err != nil {
		t.Logf("GetOrOpenDB returned error (expected if driver not registered): %v", err)
	}

	if !sm.resolved {
		t.Errorf("SecretManager.GetSecret was not called")
	}
}

// keyedSecretManager answers from a map, the way Vault or AWS would.
type keyedSecretManager map[string]string

func (m keyedSecretManager) Get(ctx context.Context, key string) (string, error) {
	return m[key], nil
}

// runSetValue previews one `set` column through the registry -- the path the
// editor's Test button and Live Preview take -- and returns what it wrote.
func runSetValue(t *testing.T, reg *Registry, expr string) any {
	t.Helper()
	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetData("id", 1)
	out, err := reg.TestTransformationPipeline(t.Context(), []storage.Transformation{{
		Type:   "set",
		Config: map[string]any{"transType": "set", "column.out": expr},
	}}, msg)
	if err != nil {
		t.Fatalf("preview %s: %v", expr, err)
	}
	if len(out) == 0 || out[0] == nil {
		t.Fatalf("preview %s returned no message", expr)
	}
	return out[0].Data()["out"]
}

// secret() in an expression reads the secret manager the operator configured.
// It read os.Getenv, so a deployment keeping its keys in Vault could not use it
// at all -- and every expression could read the process environment instead.
func TestExpressionSecretsReadTheConfiguredSecretManager(t *testing.T) {
	t.Cleanup(func() { evaluator.SetSecretSource(nil) })
	reg := NewRegistry(nil)
	reg.SetSecretManager(keyedSecretManager{"PANMAIL_API_KEY": "from-vault"})

	for _, expr := range []string{"secret('PANMAIL_API_KEY')", "env('PANMAIL_API_KEY')"} {
		if got := runSetValue(t, reg, expr); got != "from-vault" {
			t.Errorf("%s = %#v, want \"from-vault\"", expr, got)
		}
	}
}

// Whoever can edit a workflow cannot read the process environment through an
// expression. env('HERMOD_JWT_SECRET') in a set value returned the JWT signing
// key through the editor's Test button.
func TestExpressionSecretsCannotReadTheProcessEnvironment(t *testing.T) {
	t.Cleanup(func() { evaluator.SetSecretSource(nil) })
	t.Setenv("HERMOD_REGISTRY_TEST_JWT", "signing-key")
	t.Setenv("HERMOD_SECRET_REGISTRY_TEST_TOKEN", "token")
	reg := NewRegistry(nil)

	for _, expr := range []string{"env('HERMOD_REGISTRY_TEST_JWT')", "secret('HERMOD_REGISTRY_TEST_JWT')"} {
		if got := runSetValue(t, reg, expr); got == "signing-key" {
			t.Errorf("%s read the process environment", expr)
		}
	}
	if got := runSetValue(t, reg, "secret('REGISTRY_TEST_TOKEN')"); got != "token" {
		t.Errorf("secret('REGISTRY_TEST_TOKEN') = %#v, want the HERMOD_SECRET_ variable", got)
	}
}
