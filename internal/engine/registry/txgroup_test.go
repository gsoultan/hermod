package registry

import (
	"context"
	"strings"
	"testing"

	"github.com/user/hermod/internal/factory"
)

// ---------------------------------------------------------------------------
// Wiring the transactional sink group.
//
// These cover the refusals rather than the happy path, which needs two real
// PostgreSQL sinks and lives in pkg/comm/sink/txgroup's integration tests. The
// refusals are what keep the feature safe: every one of them is a case where
// starting anyway would look fine and then lose or half-apply data.
// ---------------------------------------------------------------------------

func TestTxGroupNeedsAtLeastTwoMembers(t *testing.T) {
	r := &Registry{}

	for _, members := range []string{"", "only-one", " , ", "a,,"} {
		_, err := r.createTxGroupSink(context.Background(), factory.SinkConfig{
			ID:     "grp",
			Type:   "txgroup",
			Config: map[string]string{"members": members},
		})
		if err == nil {
			t.Errorf("members=%q was accepted; a group of fewer than two is not a distributed transaction", members)
			continue
		}
		if !strings.Contains(err.Error(), "at least two") {
			t.Errorf("members=%q: error should explain the minimum, got: %v", members, err)
		}
	}
}

// TestTxGroupRefusesWithoutADurableStore is the precondition the whole design
// rests on. Falling back to an in-memory log would produce a group that works
// perfectly until the process dies, and then strands prepared transactions
// holding locks with nothing able to resolve them.
func TestTxGroupRefusesWithoutADurableStore(t *testing.T) {
	r := &Registry{} // no state store

	_, err := r.createTxGroupSink(context.Background(), factory.SinkConfig{
		ID:     "grp",
		Type:   "txgroup",
		Config: map[string]string{"members": "a,b"},
	})
	if err == nil {
		t.Fatal("a group was created with no durable transaction log")
	}
	if !strings.Contains(err.Error(), "state store") {
		t.Errorf("the error should name the missing state store, got: %v", err)
	}
}

func TestTxGroupRejectsAMalformedMaxPreparedAge(t *testing.T) {
	r := &Registry{stateStore: newFakeStateStore()}

	_, err := r.createTxGroupSink(context.Background(), factory.SinkConfig{
		ID:   "grp",
		Type: "txgroup",
		Config: map[string]string{
			"members":          "a,b",
			"max_prepared_age": "fifteen minutes",
		},
	})
	if err == nil {
		t.Fatal("an unparseable max_prepared_age was accepted")
	}
	if !strings.Contains(err.Error(), "max_prepared_age") {
		t.Errorf("the error should name the offending field, got: %v", err)
	}
}

// TestTxGroupRequiresAnID: the ID scopes the coordinator's log keys, so two
// groups sharing one would read each other's in-doubt transactions.
func TestTxGroupRequiresAnID(t *testing.T) {
	r := &Registry{stateStore: newFakeStateStore()}

	_, err := r.createTxGroupSink(context.Background(), factory.SinkConfig{
		Type:   "txgroup",
		Config: map[string]string{"members": "a,b"},
	})
	if err == nil {
		t.Fatal("a group with no ID was accepted; its transaction log would be unscoped")
	}
}

func TestSplitMemberIDs(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"a,b", 2},
		{" a , b ", 2},
		{"a,,b", 2},
		{"a,b,", 2},
		{"", 0},
		{" , ", 0},
	}
	for _, tc := range cases {
		if got := len(splitMemberIDs(tc.in)); got != tc.want {
			t.Errorf("splitMemberIDs(%q) returned %d ids, want %d", tc.in, got, tc.want)
		}
	}
}

// fakeStateStore is a hermod.StateStore that holds nothing. It exists so the
// tests above can get past the durability check to reach the case they are
// actually about.
type fakeStateStore struct{ kv map[string][]byte }

func newFakeStateStore() *fakeStateStore { return &fakeStateStore{kv: map[string][]byte{}} }

func (f *fakeStateStore) Get(_ context.Context, k string) ([]byte, error) {
	v, ok := f.kv[k]
	if !ok {
		return nil, errNotFoundForTest
	}
	return v, nil
}
func (f *fakeStateStore) Set(_ context.Context, k string, v []byte) error { f.kv[k] = v; return nil }
func (f *fakeStateStore) Delete(_ context.Context, k string) error        { delete(f.kv, k); return nil }

var errNotFoundForTest = errNotFound{}

type errNotFound struct{}

func (errNotFound) Error() string { return "not found" }
