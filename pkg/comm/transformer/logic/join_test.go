package logic

import (
	"context"
	"strings"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

type mapStateStore struct{ m map[string][]byte }

func (s *mapStateStore) Get(_ context.Context, k string) ([]byte, error) { return s.m[k], nil }
func (s *mapStateStore) Set(_ context.Context, k string, v []byte) error {
	s.m[k] = v
	return nil
}
func (s *mapStateStore) Delete(_ context.Context, k string) error {
	delete(s.m, k)
	return nil
}

func withStateStore(t *testing.T, store hermod.StateStore) context.Context {
	return context.WithValue(t.Context(), hermod.StateStoreKey, store)
}

func record(t *testing.T, fields map[string]any) hermod.Message {
	t.Helper()
	msg := message.AcquireMessage()
	for k, v := range fields {
		msg.SetData(k, v)
	}
	return msg
}

// The editor shows a new Join / Enrich node with "Lookup" selected
// (JoinFieldsConfig renders `config.mode || 'lookup'`) but saves no mode until
// the user changes it. The engine matched neither case on an empty mode, so
// such a node passed every record through untouched, with no error -- while
// its editor said it was looking records up.
func TestJoinWithNoModeLooksUp(t *testing.T) {
	ctx := withStateStore(t, &mapStateStore{m: map[string][]byte{}})
	tr := &JoinTransformer{}

	if _, err := tr.Transform(ctx, record(t, map[string]any{"id": "c1", "city": "Jakarta"}),
		map[string]any{"mode": "store", "key": "id"}); err != nil {
		t.Fatalf("store: %v", err)
	}

	// Configured the way the editor saves a new node: no mode at all.
	out, err := tr.Transform(ctx, record(t, map[string]any{"id": "c1", "total": 10}),
		map[string]any{"key": "id"})
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got := out.Data()["joined_city"]; got != "Jakarta" {
		t.Errorf("joined_city = %#v, want \"Jakarta\": a node the editor shows as Lookup did not look up", got)
	}
}

// A mode that is neither store nor lookup also matched no case and passed
// records through. A typo in a config is a fault to report, not a no-op.
func TestJoinRefusesAModeItDoesNotHave(t *testing.T) {
	ctx := withStateStore(t, &mapStateStore{m: map[string][]byte{}})
	_, err := (&JoinTransformer{}).Transform(ctx, record(t, map[string]any{"id": "c1"}),
		map[string]any{"mode": "stroe", "key": "id"})
	if err == nil || !strings.Contains(err.Error(), `"stroe"`) {
		t.Errorf("err = %v, want an error naming the mode \"stroe\"", err)
	}
}

// Without a configured state store the node cannot work at all, in a preview
// or a running workflow. "state store not available" did not say that one can
// be configured, or where.
func TestJoinWithoutAStateStoreSaysWhereToConfigureOne(t *testing.T) {
	_, err := (&JoinTransformer{}).Transform(t.Context(), record(t, map[string]any{"id": "c1"}),
		map[string]any{"mode": "lookup", "key": "id"})
	if err == nil || !strings.Contains(err.Error(), "Global State Store") {
		t.Errorf("err = %v, want it to point at Settings -> Platform -> Global State Store", err)
	}
}
