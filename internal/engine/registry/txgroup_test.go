package registry

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/engine/registry/interfaces"
	"github.com/user/hermod/internal/factory"
	"github.com/user/hermod/internal/storage"
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

// fakeStateStore is a hermod.StateStore held in memory.
//
// A missing key returns (nil, nil), which is what every real implementation
// does — Redis maps redis.Nil, etcd an empty result, and SQLite sql.ErrNoRows,
// all to no value and no error. It returned an error instead, which is a fake
// being stricter than the thing it stands for: the first start of a group has no
// transaction log, so recovery read a missing index and reported it as unread
// rather than as empty. Nothing in production behaves that way, so the tests
// were exercising a case that cannot happen while missing the one that always
// does.
type fakeStateStore struct{ kv map[string][]byte }

func newFakeStateStore() *fakeStateStore { return &fakeStateStore{kv: map[string][]byte{}} }

func (f *fakeStateStore) Get(_ context.Context, k string) ([]byte, error) {
	return f.kv[k], nil
}
func (f *fakeStateStore) Set(_ context.Context, k string, v []byte) error { f.kv[k] = v; return nil }
func (f *fakeStateStore) Delete(_ context.Context, k string) error        { delete(f.kv, k); return nil }

// ---------------------------------------------------------------------------
// Members built from stored configuration.
//
// The refusal tests above never resolve a member, and the happy-path integration
// test constructs its sinks directly. Between those two lies the path every real
// workflow takes — read the sink from storage, build it, hand it to the group —
// and it was broken: createSinkInternal applies the tracing and retry
// decorators, which forward Write but not Begin, Commit, Prepare or
// CommitPrepared, so the group rejected every member for not implementing
// hermod.TwoPhaseCommit.
//
// The feature therefore worked in tests and failed at startup everywhere else.
// ---------------------------------------------------------------------------

type txGroupSinkStore struct {
	interfaces.RegistryStorage
	sinks map[string]storage.Sink
}

func (s *txGroupSinkStore) GetSink(_ context.Context, id string) (storage.Sink, error) {
	snk, ok := s.sinks[id]
	if !ok {
		return storage.Sink{}, fmt.Errorf("no sink %q", id)
	}
	return snk, nil
}

func registryWithSinks(sinks map[string]storage.Sink) *Registry {
	return &Registry{
		stateStore: newFakeStateStore(),
		storage:    &txGroupSinkStore{sinks: sinks},
		sinkCache:  map[string]storage.Sink{},
	}
}

func postgresSink(id string) storage.Sink {
	return storage.Sink{
		ID:     id,
		Type:   "postgres",
		Config: map[string]string{"conn": "postgres://u:p@127.0.0.1:1/d", "table": "t"},
	}
}

func TestTxGroupBuildsMembersThatCanActuallyPrepare(t *testing.T) {
	r := registryWithSinks(map[string]storage.Sink{"a": postgresSink("a")})

	snk, err := r.resolveAndCreateTxGroupMember(context.Background(), "a")
	if err != nil {
		t.Fatalf("resolving a PostgreSQL member: %v", err)
	}
	defer func() { _ = snk.Close() }()

	// The whole defect in one assertion. Building the member through the
	// ordinary sink path wrapped it in decorators that forward Write but not the
	// transaction methods, so the group rejected it and no configured workflow
	// could start a group at all.
	//
	// Preflight, which asks the database whether it will really prepare, needs a
	// live PostgreSQL and is covered by the integration suite.
	if _, ok := snk.(hermod.TwoPhaseCommit); !ok {
		t.Errorf("a member built from stored configuration is %T, which does not implement "+
			"hermod.TwoPhaseCommit; every group built from configuration would be "+
			"refused at startup", snk)
	}
}

// TestTxGroupNamesAnIneligibleMember. Reporting a missing Go interface tells an
// operator nothing about what to change; the sink type and the eligible types
// are what they can act on.
func TestTxGroupNamesAnIneligibleMember(t *testing.T) {
	r := registryWithSinks(map[string]storage.Sink{
		"a": postgresSink("a"),
		"k": {ID: "k", Type: "kafka", Config: map[string]string{"brokers": "127.0.0.1:1", "topic": "t"}},
	})

	_, err := r.createTxGroupSink(context.Background(), factory.SinkConfig{
		ID:     "grp",
		Type:   "txgroup",
		Config: map[string]string{"members": "a,k"},
	})
	if err == nil {
		t.Fatal("a Kafka sink was accepted into a two-phase commit group")
	}
	for _, want := range []string{"kafka", "two-phase", "postgres"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error should mention %q so an operator knows what to change, got: %v", want, err)
		}
	}
}
