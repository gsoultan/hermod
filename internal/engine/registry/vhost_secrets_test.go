package registry

import (
	"context"
	"sync"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
)

// secretStorage is the registry's storage with a vhost secret store on it, the
// way the SQL and MongoDB backends are.
type secretStorage struct {
	mockStorage
	mu      sync.Mutex
	secrets map[string]string // "vhost/name" -> value
}

func (s *secretStorage) set(vhost, name, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.secrets[vhost+"/"+name] = value
}

func (s *secretStorage) ListVHostSecrets(context.Context, string) ([]storage.VHostSecret, error) {
	return nil, nil
}

func (s *secretStorage) GetVHostSecret(_ context.Context, vhost, name string) (storage.VHostSecret, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.secrets[vhost+"/"+name]
	if !ok {
		return storage.VHostSecret{}, storage.ErrNotFound
	}
	return storage.VHostSecret{VHost: vhost, Name: name, Value: v}, nil
}

func (s *secretStorage) PutVHostSecret(context.Context, storage.VHostSecret) error { return nil }
func (s *secretStorage) DeleteVHostSecret(context.Context, string, string) error   { return nil }
func (s *secretStorage) DeleteVHostSecrets(context.Context, string) error          { return nil }

func newSecretRegistry(t *testing.T) (*Registry, *secretStorage) {
	t.Helper()
	t.Cleanup(func() { evaluator.SetSecretSource(nil) })
	st := &secretStorage{secrets: map[string]string{
		"tenant-a/API_KEY": "a-key",
		"tenant-b/API_KEY": "b-key",
		"tenant-b/ONLY_B":  "b-only",
	}}
	return NewRegistry(st), st
}

// previewSecret runs one `set` column through the registry's preview path as a
// message of the given vhost, and returns what the column wrote.
func previewSecret(t *testing.T, reg *Registry, vhost, expr string) any {
	t.Helper()
	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetData("id", 1)
	msg.SetVHost(vhost)
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

// A workflow reads the secrets of its own vhost, saved in Hermod's storage, and
// nobody else's. This goes from stored secrets through the registry and the
// transformer, the way the editor's Test button and a running workflow do.
func TestAWorkflowReadsOnlyItsOwnVHostsSecrets(t *testing.T) {
	t.Setenv("HERMOD_SECRET_GLOBAL_KEY", "from-env")
	reg, _ := newSecretRegistry(t)

	tests := []struct {
		name  string
		vhost string
		expr  string
		want  any
	}{
		{"its own secret", "tenant-a", "secret('API_KEY')", "a-key"},
		{"the same name in another vhost", "tenant-b", "secret('API_KEY')", "b-key"},
		{"another vhost's secret", "tenant-a", "secret('ONLY_B')", ""},
		{"a global secret still resolves", "tenant-a", "secret('GLOBAL_KEY')", "from-env"},
		{"a message with no vhost reads no vhost's secret", "", "secret('API_KEY')", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := previewSecret(t, reg, tc.vhost, tc.expr); got != tc.want {
				t.Errorf("%s in %q = %#v, want %#v", tc.expr, tc.vhost, got, tc.want)
			}
		})
	}
}

// Rotating a secret is seen by the next message. The expression cache would
// otherwise keep serving the old value for a minute.
func TestARotatedVHostSecretIsReadWithoutARestart(t *testing.T) {
	reg, st := newSecretRegistry(t)
	if got := previewSecret(t, reg, "tenant-a", "secret('API_KEY')"); got != "a-key" {
		t.Fatalf("before rotation = %#v, want a-key", got)
	}

	st.set("tenant-a", "API_KEY", "rotated")
	reg.InvalidateVHostSecret("tenant-a", "API_KEY")

	if got := previewSecret(t, reg, "tenant-a", "secret('API_KEY')"); got != "rotated" {
		t.Errorf("after rotation = %#v, want rotated", got)
	}
	if got := previewSecret(t, reg, "tenant-b", "secret('API_KEY')"); got != "b-key" {
		t.Errorf("rotating tenant-a's secret changed tenant-b's: %#v", got)
	}
}

// A connector's `secret:NAME` is resolved for the vhost the connector belongs
// to, when it is built.
func TestAConnectorResolvesSecretsOfItsOwnVHost(t *testing.T) {
	reg, _ := newSecretRegistry(t)

	for vhost, want := range map[string]string{"tenant-a": "a-key", "tenant-b": "b-key", "tenant-c": "secret:API_KEY"} {
		got := reg.resolveSecrets(t.Context(), vhost, map[string]string{"password": "secret:API_KEY", "host": "db"})
		if got["password"] != want || got["host"] != "db" {
			t.Errorf("vhost %q resolved %v, want password %q", vhost, got, want)
		}
	}
}

// Run Simulation walks a workflow that belongs to a vhost, so its messages read
// that vhost's secrets -- the same answer the running workflow gives.
func TestASimulationReadsTheWorkflowsVHostSecrets(t *testing.T) {
	// Real storage here: the secret is saved the way the API saves it, and the
	// simulation needs its source to exist.
	t.Cleanup(func() { evaluator.SetSecretSource(nil) })
	reg := newSimRegistry(t)
	store, ok := reg.store().(storage.VHostSecretStore)
	if !ok {
		t.Fatal("the SQL store does not implement storage.VHostSecretStore")
	}
	for vhost, value := range map[string]string{"tenant-a": "a-key", "tenant-b": "b-key"} {
		if err := store.PutVHostSecret(t.Context(), storage.VHostSecret{VHost: vhost, Name: "API_KEY", Value: value}); err != nil {
			t.Fatalf("PutVHostSecret: %v", err)
		}
	}
	wf := storage.Workflow{
		ID: "wf-sim", VHost: "tenant-b",
		Nodes: []storage.WorkflowNode{
			{ID: "src", Type: "source", RefID: "src-1"},
			{ID: "t1", Type: "transformation", Config: map[string]any{
				"transType": "set", "column.key": "secret('API_KEY')",
			}},
			{ID: "snk", Type: "sink", RefID: "snk-1"},
		},
		Edges: []storage.WorkflowEdge{
			{ID: "e1", SourceID: "src", TargetID: "t1"},
			{ID: "e2", SourceID: "t1", TargetID: "snk"},
		},
	}
	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetData("id", 1)

	steps, err := reg.TestWorkflow(t.Context(), wf, msg)
	if err != nil {
		t.Fatalf("TestWorkflow: %v", err)
	}
	var got any
	for _, s := range steps {
		if s.NodeID == "t1" {
			got = s.Payload["key"]
		}
	}
	if got != "b-key" {
		t.Errorf("the simulated set node wrote key = %#v, want b-key (steps: %+v)", got, steps)
	}
}

// A node may answer with a message it built from scratch rather than a clone.
// That one has no mark of its own, and takes its input's; one that already has
// a mark keeps it.
func TestANewMessageFromANodeInheritsTheVHost(t *testing.T) {
	in := message.AcquireMessage()
	t.Cleanup(in.Release)
	in.SetVHost("tenant-a")

	fresh := message.AcquireMessage()
	t.Cleanup(fresh.Release)
	marked := message.AcquireMessage()
	t.Cleanup(marked.Release)
	marked.SetVHost("tenant-b")

	inheritVHost(in, []hermod.Message{fresh, marked, nil, in})

	if got := fresh.VHost(); got != "tenant-a" {
		t.Errorf("a new message has vhost %q, want tenant-a", got)
	}
	if got := marked.VHost(); got != "tenant-b" {
		t.Errorf("an already marked message changed to %q, want tenant-b", got)
	}
}

// A node that produces new messages hands them on in the same workflow, so
// they keep the vhost of the message that produced them.
func TestANodesOutputKeepsTheVHost(t *testing.T) {
	reg, _ := newSecretRegistry(t)
	in := message.AcquireMessage()
	t.Cleanup(in.Release)
	in.SetVHost("tenant-a")
	in.SetData("items", []any{map[string]any{"n": 1}, map[string]any{"n": 2}})

	node := &storage.WorkflowNode{ID: "n1", Type: "foreach", Config: map[string]any{"arrayPath": "items"}}
	out, _, err := reg.RunWorkflowNode("wf-1", node, in)
	if err != nil {
		t.Fatalf("RunWorkflowNode: %v", err)
	}
	if len(out) < 2 {
		t.Fatalf("foreach returned %d messages, want one per item", len(out))
	}
	for i, m := range out {
		scoped, ok := m.(hermod.VHostScoped)
		if !ok || scoped.VHost() != "tenant-a" {
			t.Errorf("output %d has vhost %v, want tenant-a", i, m)
		}
	}
}
