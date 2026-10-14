package sql

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
)

func deadLetterStore(t *testing.T) storage.DeadLetterStore {
	t.Helper()
	var st storage.Storage = newRotationStorage(t)
	ds, ok := st.(storage.DeadLetterStore)
	if !ok {
		t.Fatal("the SQL store does not implement DeadLetterStore")
	}
	return ds
}

func sampleDeadLetter(id, vhost, wf string, at time.Time) storage.DeadLetter {
	return storage.DeadLetter{
		ID: id, VHost: vhost, WorkflowID: wf, MessageID: "msg-" + id,
		NodeID: "normalize", Error: `field total is not a number: got "12,50"`, FailedAt: at,
		Payload:  []byte(`{"id":"ord-1","total":"12,50"}`),
		Metadata: map[string]string{"_hermod_failed_node": "normalize", "table": "orders"},
		Origin:   storage.DeadLetterOrigin{Operation: "update", Table: "orders", Before: []byte(`{"id":"ord-1","total":"12.00"}`)},
		History:  []storage.DeadLetterEvent{{At: at, Action: "failed", Detail: "normalize"}},
	}
}

func TestDeadLetterRoundTrips(t *testing.T) {
	st := deadLetterStore(t)
	ctx := t.Context()
	at := time.Date(2026, 10, 14, 4, 52, 55, 0, time.UTC)

	if _, err := st.GetDeadLetter(ctx, "dl-1"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("a missing dead letter: err = %v, want ErrNotFound", err)
	}
	if err := st.PutDeadLetter(ctx, sampleDeadLetter("dl-1", "tenant-a", "wf-1", at)); err != nil {
		t.Fatalf("PutDeadLetter: %v", err)
	}
	got, err := st.GetDeadLetter(ctx, "dl-1")
	if err != nil {
		t.Fatalf("GetDeadLetter: %v", err)
	}
	if got.VHost != "tenant-a" || got.WorkflowID != "wf-1" || got.MessageID != "msg-dl-1" || got.NodeID != "normalize" ||
		!strings.Contains(got.Error, "12,50") || !got.FailedAt.Equal(at) || got.Status != storage.DeadLetterQueued ||
		string(got.Payload) != `{"id":"ord-1","total":"12,50"}` || got.PayloadSize != len(got.Payload) ||
		got.Metadata["table"] != "orders" || got.Origin.Operation != "update" || string(got.Origin.Before) != `{"id":"ord-1","total":"12.00"}` || len(got.History) != 1 {
		t.Fatalf("round trip lost fields: %+v", got)
	}
}

func TestDeadLetterPayloadIsCappedAndMarkedTruncated(t *testing.T) {
	st := deadLetterStore(t)
	ctx := t.Context()
	d := sampleDeadLetter("big", "", "wf-1", time.Now())
	d.Payload = bytes.Repeat([]byte("x"), storage.DeadLetterPayloadCap+10)
	if err := st.PutDeadLetter(ctx, d); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetDeadLetter(ctx, "big")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Truncated || len(got.Payload) != storage.DeadLetterPayloadCap || got.PayloadSize != storage.DeadLetterPayloadCap+10 {
		t.Fatalf("truncated=%v len=%d size=%d", got.Truncated, len(got.Payload), got.PayloadSize)
	}
}

func TestDeadLetterListFiltersPagesAndKeepsToItsVHosts(t *testing.T) {
	st := deadLetterStore(t)
	ctx := t.Context()
	base := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	for i := range 5 {
		d := sampleDeadLetter(fmt.Sprintf("a%d", i), "tenant-a", "wf-orders", base.Add(time.Duration(i)*time.Minute))
		if i == 4 {
			d.NodeID, d.SinkID, d.Error = "", "sink-warehouse", "connection refused"
		}
		if err := st.PutDeadLetter(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.PutDeadLetter(ctx, sampleDeadLetter("b0", "tenant-b", "wf-billing", base)); err != nil {
		t.Fatal(err)
	}

	page, total, err := st.ListDeadLetters(ctx, storage.DeadLetterFilter{VHosts: []string{"tenant-a"}, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if total != 5 || len(page) != 2 || page[0].ID != "a4" || page[1].ID != "a3" {
		t.Fatalf("first page = %v (total %d), want a4, a3 of 5", ids(page), total)
	}
	if page[0].Payload != nil || page[0].History != nil {
		t.Fatal("a list page should leave the payload and history out")
	}
	page, _, _ = st.ListDeadLetters(ctx, storage.DeadLetterFilter{VHosts: []string{"tenant-a"}, Limit: 2, Offset: 4})
	if len(page) != 1 || page[0].ID != "a0" {
		t.Fatalf("last page = %v, want a0", ids(page))
	}

	cases := []struct {
		name string
		f    storage.DeadLetterFilter
		want int
	}{
		{"every vhost", storage.DeadLetterFilter{}, 6},
		{"by workflow", storage.DeadLetterFilter{WorkflowID: "wf-billing"}, 1},
		{"by sink", storage.DeadLetterFilter{Where: "sink-warehouse"}, 1},
		{"by node", storage.DeadLetterFilter{Where: "normalize"}, 5},
		{"search error, any case", storage.DeadLetterFilter{Search: "CONNECTION"}, 1},
		{"search message id", storage.DeadLetterFilter{Search: "msg-a2"}, 1},
		{"search treats % as text", storage.DeadLetterFilter{Search: "%"}, 0},
		{"since", storage.DeadLetterFilter{Since: base.Add(3 * time.Minute)}, 2},
		{"until (a0, a1 and b0)", storage.DeadLetterFilter{Until: base.Add(time.Minute)}, 3},
		{"status", storage.DeadLetterFilter{Status: storage.DeadLetterDiscarded}, 0},
		{"another vhost", storage.DeadLetterFilter{VHosts: []string{"tenant-c"}}, 0},
	}
	for _, c := range cases {
		_, total, err := st.ListDeadLetters(ctx, c.f)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if total != c.want {
			t.Errorf("%s: total = %d, want %d", c.name, total, c.want)
		}
	}
}

func ids(ds []storage.DeadLetter) []string {
	out := make([]string, len(ds))
	for i, d := range ds {
		out[i] = d.ID
	}
	return out
}

func TestDeadLetterSettlesOnce(t *testing.T) {
	st := deadLetterStore(t)
	ctx := t.Context()
	if err := st.PutDeadLetter(ctx, sampleDeadLetter("dl", "", "wf-1", time.Now())); err != nil {
		t.Fatal(err)
	}
	if err := st.SettleDeadLetter(ctx, "missing", storage.DeadLetterDiscarded, storage.NewDeadLetterEvent("discarded", "ada", "")); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("settling a missing dead letter: err = %v, want ErrNotFound", err)
	}

	// Two people pressing Replay at once must not both replay the message.
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Go(func() {
			errs[i] = st.SettleDeadLetter(ctx, "dl", storage.DeadLetterReplayed, storage.NewDeadLetterEvent("replayed", "ada", ""))
		})
	}
	wg.Wait()
	won := 0
	for _, err := range errs {
		switch {
		case err == nil:
			won++
		case !errors.Is(err, storage.ErrDeadLetterSettled):
			t.Fatalf("a losing settle: err = %v, want ErrDeadLetterSettled", err)
		}
	}
	if won != 1 {
		t.Fatalf("%d settles succeeded, want exactly 1", won)
	}

	got, _ := st.GetDeadLetter(ctx, "dl")
	if got.Status != storage.DeadLetterReplayed || got.SettledBy != "ada" || got.SettledAt == nil ||
		len(got.History) != 2 || got.History[1].Action != "replayed" {
		t.Fatalf("after settling: %+v", got)
	}
}

func TestDeadLetterEventsAddToTheHistoryWithoutSettling(t *testing.T) {
	st := deadLetterStore(t)
	ctx := t.Context()
	if err := st.PutDeadLetter(ctx, sampleDeadLetter("dl", "", "wf-1", time.Now())); err != nil {
		t.Fatal(err)
	}
	if err := st.AddDeadLetterEvent(ctx, "dl", storage.NewDeadLetterEvent("replay_failed", "ada", "timeout")); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetDeadLetter(ctx, "dl")
	if got.Status != storage.DeadLetterQueued || len(got.History) != 2 || got.History[1].Detail != "timeout" {
		t.Fatalf("after the event: %+v", got)
	}
	if err := st.AddDeadLetterEvent(ctx, "missing", storage.NewDeadLetterEvent("x", "", "")); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("missing: err = %v, want ErrNotFound", err)
	}
}

func TestDeadLetterStats(t *testing.T) {
	st := deadLetterStore(t)
	ctx := t.Context()
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	put := func(id, vhost, errText string, at time.Time) {
		d := sampleDeadLetter(id, vhost, "wf-1", at)
		d.Error = errText
		if err := st.PutDeadLetter(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	put("1", "tenant-a", "timeout", now.Add(-time.Hour))
	put("2", "tenant-a", "timeout", now.Add(-2*time.Hour))
	put("2b", "tenant-a", "timeout", now.Add(-72*time.Hour))
	put("3", "tenant-a", "bad json", now.Add(-48*time.Hour))
	put("4", "tenant-a", "bad json", now.Add(-50*time.Hour))
	put("5", "tenant-a", "bad json", now.Add(-51*time.Hour))
	put("6", "tenant-b", "bad json", now.Add(-time.Hour))
	if err := st.SettleDeadLetter(ctx, "5", storage.DeadLetterReplayed, storage.DeadLetterEvent{At: now.Add(-time.Hour), Action: "replayed"}); err != nil {
		t.Fatal(err)
	}

	got, err := st.DeadLetterStats(ctx, []string{"tenant-a"}, now)
	if err != nil {
		t.Fatal(err)
	}
	want := storage.DeadLetterStats{Queued: 5, Last24h: 2, TopError: "timeout", TopErrorCount: 3, ReplayedThisWeek: 1}
	if got != want {
		t.Fatalf("stats = %+v, want %+v", got, want)
	}
	// "bad json" has three in tenant-a too, but one was replayed and one is
	// in tenant-b: the top error is about what is still in this queue.
}

func TestDeadLetterPurgeRemovesOnlyOlderOnes(t *testing.T) {
	st := deadLetterStore(t)
	ctx := t.Context()
	now := time.Now().UTC()
	for i, age := range []time.Duration{time.Hour, 10 * 24 * time.Hour, 40 * 24 * time.Hour} {
		if err := st.PutDeadLetter(ctx, sampleDeadLetter(fmt.Sprint(i), "", "wf", now.Add(-age))); err != nil {
			t.Fatal(err)
		}
	}
	n, err := st.PurgeDeadLetters(ctx, now.Add(-30*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("purged %d, want 1", n)
	}
	if _, total, _ := st.ListDeadLetters(ctx, storage.DeadLetterFilter{}); total != 2 {
		t.Fatalf("%d left, want 2", total)
	}
}

func TestDeadLetterPutRejectsWhatItCannotKeep(t *testing.T) {
	st := deadLetterStore(t)
	d := sampleDeadLetter("", "", "wf", time.Now())
	if err := st.PutDeadLetter(t.Context(), d); err == nil {
		t.Fatal("a dead letter with no id was kept")
	}
}

// SQL Server has no LIMIT; the page has to be asked for its own way there.
func TestDeadLetterPageClausePerDialect(t *testing.T) {
	for driver, want := range map[string]string{
		"sqlite":    " LIMIT ? OFFSET ?",
		"pgx":       " LIMIT ? OFFSET ?",
		"mysql":     " LIMIT ? OFFSET ?",
		"sqlserver": " OFFSET ? ROWS FETCH NEXT ? ROWS ONLY",
	} {
		s := &sqlStorage{driver: driver}
		clause, args := s.pageClause(10, 20)
		if clause != want {
			t.Errorf("%s: clause = %q, want %q", driver, clause, want)
		}
		wantArgs := []any{10, 20}
		if driver == "sqlserver" {
			wantArgs = []any{20, 10}
		}
		if fmt.Sprint(args) != fmt.Sprint(wantArgs) {
			t.Errorf("%s: args = %v, want %v", driver, args, wantArgs)
		}
	}
}
