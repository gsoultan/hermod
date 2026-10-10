package features

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// memStore is a StateStore over a map. failSet makes every Set fail.
type memStore struct {
	mu      sync.Mutex
	m       map[string][]byte
	sets    int
	failSet bool
}

func newMemStore() *memStore { return &memStore{m: map[string][]byte{}} }

func (s *memStore) Get(_ context.Context, key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.m[key], nil
}

func (s *memStore) Set(_ context.Context, key string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sets++
	if s.failSet {
		return errors.New("disk full")
	}
	s.m[key] = append([]byte(nil), value...)
	return nil
}

func (s *memStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, key)
	return nil
}

// clock is a settable time source.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func newClock() *clock { return &clock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)} }

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// event runs tr over one record as node in workflow, with store (may be nil)
// on the context.
func event(t *testing.T, tr interface {
	Transform(context.Context, hermod.Message, map[string]any) (hermod.Message, error)
}, workflow, node string, store hermod.StateStore, cfg, fields map[string]any) map[string]any {
	t.Helper()
	out, err := eventErr(t, tr, workflow, node, store, cfg, fields)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	return out
}

func eventErr(t *testing.T, tr interface {
	Transform(context.Context, hermod.Message, map[string]any) (hermod.Message, error)
}, workflow, node string, store hermod.StateStore, cfg, fields map[string]any) (map[string]any, error) {
	t.Helper()
	ctx := context.WithValue(t.Context(), hermod.NodeIDKey, node)
	if store != nil {
		ctx = context.WithValue(ctx, hermod.StateStoreKey, store)
	}
	msg := message.AcquireMessage()
	defer msg.Release()
	msg.SetMetadata("_hermod_workflow_id", workflow)
	for k, v := range fields {
		msg.SetData(k, v)
	}
	out, err := tr.Transform(ctx, msg, cfg)
	if err != nil || out == nil {
		return nil, err
	}
	data := map[string]any{}
	for k, v := range out.Data() {
		data[k] = v
	}
	return data, nil
}

func wantFloat(t *testing.T, data map[string]any, field string, want float64) {
	t.Helper()
	got, ok := data[field].(float64)
	if !ok || math.Abs(got-want) > 1e-9 {
		t.Errorf("%s = %#v, want %v", field, data[field], want)
	}
}

func TestRollingOverTheLastNEventsPerKey(t *testing.T) {
	r := &Rolling{}
	cfg := map[string]any{"field": "amount", "keyBy": "customer", "windowType": "count", "size": 3.0}

	for _, v := range []float64{1, 2, 3} {
		event(t, r, "wf", "n1", nil, cfg, map[string]any{"customer": "a", "amount": v})
	}
	// Another key does not disturb a's window.
	other := event(t, r, "wf", "n1", nil, cfg, map[string]any{"customer": "b", "amount": 100.0})
	wantFloat(t, other, "amount_count", 1)
	wantFloat(t, other, "amount_mean", 100)

	got := event(t, r, "wf", "n1", nil, cfg, map[string]any{"customer": "a", "amount": 4.0})
	// The window is now [2, 3, 4]: the current record is included.
	wantFloat(t, got, "amount_count", 3)
	wantFloat(t, got, "amount_sum", 9)
	wantFloat(t, got, "amount_mean", 3)
	wantFloat(t, got, "amount_min", 2)
	wantFloat(t, got, "amount_max", 4)
	wantFloat(t, got, "amount_std", math.Sqrt(2.0/3.0))
}

func TestRollingOverATimeWindow(t *testing.T) {
	c := newClock()
	r := &Rolling{now: c.Now}
	cfg := map[string]any{"field": "v", "windowType": "time", "window": "1m", "features": []any{"count", "sum"}}

	event(t, r, "wf", "n1", nil, cfg, map[string]any{"v": 1.0})
	c.Advance(30 * time.Second)
	event(t, r, "wf", "n1", nil, cfg, map[string]any{"v": 2.0})
	c.Advance(40 * time.Second) // the first event is now 70s old
	got := event(t, r, "wf", "n1", nil, cfg, map[string]any{"v": 4.0})

	wantFloat(t, got, "v_count", 2)
	wantFloat(t, got, "v_sum", 6)
	if _, ok := got["v_mean"]; ok {
		t.Error("v_mean was written though only count and sum were asked for")
	}
}

func TestRollingTimeWindowIsCappedByMaxEvents(t *testing.T) {
	r := &Rolling{now: newClock().Now}
	cfg := map[string]any{"field": "v", "windowType": "time", "window": "1h", "maxEvents": 5.0, "features": "count, min"}
	var got map[string]any
	for i := range 20 {
		got = event(t, r, "wf", "n1", nil, cfg, map[string]any{"v": float64(i)})
	}
	wantFloat(t, got, "v_count", 5)
	wantFloat(t, got, "v_min", 15)
}

func TestRollingCountsEventsWithoutAField(t *testing.T) {
	r := &Rolling{}
	cfg := map[string]any{"keyBy": "user", "size": 10.0, "features": []any{"count"}, "prefix": "logins_"}
	var got map[string]any
	for range 4 {
		got = event(t, r, "wf", "n1", nil, cfg, map[string]any{"user": "u1"})
	}
	wantFloat(t, got, "logins_count", 4)
}

func TestRollingStateIsPerWorkflowAndNode(t *testing.T) {
	r := &Rolling{}
	cfg := map[string]any{"field": "v", "size": 10.0, "features": []any{"count"}}
	event(t, r, "wf1", "n1", nil, cfg, map[string]any{"v": 1.0})
	event(t, r, "wf1", "n1", nil, cfg, map[string]any{"v": 1.0})
	wantFloat(t, event(t, r, "wf1", "n2", nil, cfg, map[string]any{"v": 1.0}), "v_count", 1)
	wantFloat(t, event(t, r, "wf2", "n1", nil, cfg, map[string]any{"v": 1.0}), "v_count", 1)
	wantFloat(t, event(t, r, "wf1", "n1", nil, cfg, map[string]any{"v": 1.0}), "v_count", 3)
}

func TestRollingEvictsTheLeastRecentlySeenKey(t *testing.T) {
	r := &Rolling{}
	cfg := map[string]any{"field": "v", "keyBy": "k", "size": 10.0, "maxKeys": 2.0, "features": []any{"count"}}
	event(t, r, "wf", "n1", nil, cfg, map[string]any{"k": "a", "v": 1.0})
	event(t, r, "wf", "n1", nil, cfg, map[string]any{"k": "b", "v": 1.0})
	event(t, r, "wf", "n1", nil, cfg, map[string]any{"k": "a", "v": 1.0}) // a is now the most recent
	event(t, r, "wf", "n1", nil, cfg, map[string]any{"k": "c", "v": 1.0}) // evicts b

	wantFloat(t, event(t, r, "wf", "n1", nil, cfg, map[string]any{"k": "a", "v": 1.0}), "v_count", 3)
	wantFloat(t, event(t, r, "wf", "n1", nil, cfg, map[string]any{"k": "b", "v": 1.0}), "v_count", 1)
	if n := r.windows.keys(); n > 2 {
		t.Errorf("%d keys held, want at most maxKeys = 2", n)
	}
}

// With persistent on, a restart (a new transformer) and an eviction both
// reload a key's window from the state store.
func TestRollingPersistentStateSurvivesRestartAndEviction(t *testing.T) {
	store := newMemStore()
	cfg := map[string]any{"field": "v", "keyBy": "k", "size": 5.0, "maxKeys": 1.0, "persistent": true, "features": []any{"count", "sum"}}

	r := &Rolling{}
	event(t, r, "wf", "n1", store, cfg, map[string]any{"k": "a", "v": 1.0})
	event(t, r, "wf", "n1", store, cfg, map[string]any{"k": "a", "v": 2.0})
	event(t, r, "wf", "n1", store, cfg, map[string]any{"k": "b", "v": 7.0}) // evicts a from memory

	got := event(t, r, "wf", "n1", store, cfg, map[string]any{"k": "a", "v": 3.0})
	wantFloat(t, got, "v_count", 3)
	wantFloat(t, got, "v_sum", 6)

	restarted := &Rolling{}
	got = event(t, restarted, "wf", "n1", store, cfg, map[string]any{"k": "a", "v": 4.0})
	wantFloat(t, got, "v_count", 4)
	wantFloat(t, got, "v_sum", 10)
}

func TestRollingWithoutPersistentStartsEmptyAfterRestart(t *testing.T) {
	store := newMemStore()
	cfg := map[string]any{"field": "v", "size": 5.0, "features": []any{"count"}}
	event(t, &Rolling{}, "wf", "n1", store, cfg, map[string]any{"v": 1.0})
	if store.sets != 0 {
		t.Errorf("%d writes to the state store without persistent", store.sets)
	}
	wantFloat(t, event(t, &Rolling{}, "wf", "n1", store, cfg, map[string]any{"v": 1.0}), "v_count", 1)
}

// A time window reloaded after a restart drops what expired while it was down.
func TestRollingPersistentTimeWindowExpiresWhileDown(t *testing.T) {
	store := newMemStore()
	c := newClock()
	cfg := map[string]any{"field": "v", "windowType": "time", "window": "10m", "persistent": true, "features": []any{"count"}}
	event(t, &Rolling{now: c.Now}, "wf", "n1", store, cfg, map[string]any{"v": 1.0})
	c.Advance(11 * time.Minute)
	wantFloat(t, event(t, &Rolling{now: c.Now}, "wf", "n1", store, cfg, map[string]any{"v": 1.0}), "v_count", 1)
}

// A failed save does not fail the record: it carries correct features, and
// the in-memory window stays right.
func TestRollingSaveFailureKeepsTheRecord(t *testing.T) {
	store := newMemStore()
	store.failSet = true
	r := &Rolling{}
	cfg := map[string]any{"field": "v", "size": 5.0, "persistent": true, "features": []any{"count"}}
	event(t, r, "wf", "n1", store, cfg, map[string]any{"v": 1.0})
	wantFloat(t, event(t, r, "wf", "n1", store, cfg, map[string]any{"v": 1.0}), "v_count", 2)
}

func TestRollingMissingValue(t *testing.T) {
	r := &Rolling{}
	cfg := map[string]any{"field": "v", "size": 5.0, "features": []any{"count"}}
	if _, err := eventErr(t, r, "wf", "n1", nil, cfg, map[string]any{}); err == nil {
		t.Error("a missing value should fail the record by default")
	}
	skip := with(cfg, "onMissing", "skip")
	got := event(t, r, "wf", "n9", nil, skip, map[string]any{"v": "x"})
	if _, ok := got["v_count"]; ok {
		t.Error("skip should leave the record unchanged")
	}
	// ...and must not have entered the window.
	wantFloat(t, event(t, r, "wf", "n9", nil, skip, map[string]any{"v": 1.0}), "v_count", 1)
}

func TestRollingRejectsBadConfig(t *testing.T) {
	tests := []struct {
		name string
		cfg  map[string]any
		want string
	}{
		{"unknown window type", map[string]any{"field": "v", "windowType": "session"}, "windowType"},
		{"no size", map[string]any{"field": "v", "windowType": "count"}, "size"},
		{"size too large", map[string]any{"field": "v", "size": 1e9}, "size"},
		{"no duration", map[string]any{"field": "v", "windowType": "time"}, "window"},
		{"bad duration", map[string]any{"field": "v", "windowType": "time", "window": "soon"}, "window"},
		{"unknown feature", map[string]any{"field": "v", "size": 3.0, "features": []any{"median"}}, "median"},
		{"no field for a value feature", map[string]any{"size": 3.0, "features": []any{"mean"}}, "field"},
		{"maxKeys negative", map[string]any{"field": "v", "size": 3.0, "maxKeys": -1.0}, "maxKeys"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := (&Rolling{}).Prepare(tt.cfg)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Prepare error = %v, want one mentioning %q", err, tt.want)
			}
			if _, err := eventErr(t, &Rolling{}, "wf", "n1", nil, tt.cfg, map[string]any{"v": 1.0}); err == nil {
				t.Fatal("Transform accepted a config Prepare refused")
			}
		})
	}
}

// Many workers share one transformer. Run with -race.
func TestRollingIsSafeUnderConcurrentWorkers(t *testing.T) {
	r := &Rolling{}
	store := newMemStore()
	cfg := map[string]any{"field": "v", "keyBy": "k", "size": 10000.0, "maxKeys": 3.0, "persistent": true, "features": []any{"count", "sum", "std"}}
	const workers, each = 8, 200

	var wg sync.WaitGroup
	for w := range workers {
		wg.Go(func() {
			for i := range each {
				key := fmt.Sprintf("k%d", (w+i)%4) // four keys over three slots: eviction under load
				if _, err := eventErr(t, r, "wf", "n1", store, cfg, map[string]any{"k": key, "v": 1.0}); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	wg.Wait()

	// Every event was saved, and each key's saved window adds up to the total.
	total := 0.0
	for _, k := range []string{"k0", "k1", "k2", "k3"} {
		got := event(t, &Rolling{}, "wf", "n1", store, cfg, map[string]any{"k": k, "v": 0.0})
		n, ok := got["v_count"].(float64)
		if !ok {
			t.Fatalf("%s: v_count = %#v", k, got["v_count"])
		}
		total += n - 1
	}
	if total != workers*each {
		t.Errorf("windows hold %v events, want %d", total, workers*each)
	}
}
