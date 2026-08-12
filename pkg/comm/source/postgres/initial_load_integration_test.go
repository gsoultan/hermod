//go:build integration

package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/user/hermod"
)

// ---------------------------------------------------------------------------
// Initial load: carrying across what is already there.
//
// Starting a CDC workflow streams changes from that moment; the rows already in
// the table were never carried across, so onboarding an existing database meant
// sequencing a snapshot and a stream by hand and hoping the order was right.
// Snapshot first and every change made in between is lost, silently.
//
// The slot exports a snapshot when it is created, and the backfill reads at that
// exact point. What this has to prove is the join: every row present before, and
// every change after, with the boundary landing in neither gap nor duplicate.
// ---------------------------------------------------------------------------

// startWithInitialLoad brings the source up with the backfill enabled. It
// mirrors cdcFixture.start, which cannot be reused because the flag has to be
// set before the first Read triggers initialisation.
func (f *cdcFixture) startWithInitialLoad(t *testing.T, ctx context.Context) {
	t.Helper()

	f.source = NewPostgresSource(f.dsn, f.slot, f.pub, []string{f.table}, true, "", time.Second)
	f.source.SetInitialLoad(true)
	f.msgs = make(chan hermod.Message, 512)

	readCtx, cancel := context.WithCancel(ctx)
	f.cancel = cancel

	go func() {
		for {
			msg, err := f.source.Read(readCtx)
			if readCtx.Err() != nil {
				return
			}
			if err != nil {
				f.setErr(err)
				time.Sleep(200 * time.Millisecond)
				continue
			}
			select {
			case f.msgs <- msg:
			case <-readCtx.Done():
				return
			}
		}
	}()
}

// collectNames drains messages until every wanted name has been seen or the
// deadline passes, returning how many times each arrived.
func collectNames(t *testing.T, f *cdcFixture, want map[string]bool, timeout time.Duration) map[string]int {
	t.Helper()
	seen := map[string]int{}
	deadline := time.After(timeout)

	for {
		remaining := false
		for name := range want {
			if seen[name] == 0 {
				remaining = true
				break
			}
		}
		if !remaining {
			// Everything arrived. Keep draining briefly so a duplicate would be
			// caught rather than missed by returning too early.
			drain := time.After(2 * time.Second)
			for {
				select {
				case msg := <-f.msgs:
					if n := nameOf(msg); n != "" {
						seen[n]++
					}
					msg.Release()
				case <-drain:
					return seen
				}
			}
		}

		select {
		case msg := <-f.msgs:
			if n := nameOf(msg); n != "" {
				seen[n]++
			}
			msg.Release()
		case <-deadline:
			return seen
		}
	}
}

func nameOf(msg hermod.Message) string {
	if msg == nil {
		return ""
	}
	if v, ok := msg.Data()["name"]; ok {
		return fmt.Sprintf("%v", v)
	}
	var after map[string]any
	if err := json.Unmarshal(msg.After(), &after); err == nil {
		if v, ok := after["name"]; ok {
			return fmt.Sprintf("%v", v)
		}
	}
	return ""
}

// TestInitialLoadCarriesExistingRows is the feature in one assertion: rows that
// were there before the workflow started arrive.
func TestInitialLoadCarriesExistingRows(t *testing.T) {
	f := newCDCFixture(t)
	ctx := t.Context()

	for i := range 5 {
		mustExec(t, f.db, fmt.Sprintf("INSERT INTO %s (name) VALUES ($1)", f.table),
			fmt.Sprintf("before-%d", i))
	}

	f.startWithInitialLoad(t, ctx)

	want := map[string]bool{}
	for i := range 5 {
		want[fmt.Sprintf("before-%d", i)] = true
	}

	seen := collectNames(t, f, want, 60*time.Second)
	for name := range want {
		if seen[name] == 0 {
			t.Errorf("%s was in the table before the workflow started and never arrived; "+
				"the initial load did not carry it across", name)
		}
	}
}

// TestInitialLoadJoinsCleanlyOntoTheStream is the property the exported snapshot
// buys. Rows written after the slot exists must arrive exactly once — not twice
// because they were both snapshotted and streamed, and not never because they
// landed in the gap between the two.
func TestInitialLoadJoinsCleanlyOntoTheStream(t *testing.T) {
	f := newCDCFixture(t)
	ctx := t.Context()

	for i := range 3 {
		mustExec(t, f.db, fmt.Sprintf("INSERT INTO %s (name) VALUES ($1)", f.table),
			fmt.Sprintf("before-%d", i))
	}

	f.startWithInitialLoad(t, ctx)

	// Written after start-up, so they belong to the stream rather than the
	// backfill. This is the boundary.
	time.Sleep(2 * time.Second)
	for i := range 3 {
		mustExec(t, f.db, fmt.Sprintf("INSERT INTO %s (name) VALUES ($1)", f.table),
			fmt.Sprintf("after-%d", i))
	}

	want := map[string]bool{}
	for i := range 3 {
		want[fmt.Sprintf("before-%d", i)] = true
		want[fmt.Sprintf("after-%d", i)] = true
	}

	seen := collectNames(t, f, want, 90*time.Second)

	for name := range want {
		switch {
		case seen[name] == 0:
			t.Errorf("%s never arrived; it fell in the gap between the backfill and the stream", name)
		case seen[name] > 1:
			t.Errorf("%s arrived %d times; it was both backfilled and streamed, so the "+
				"snapshot was not taken at the slot's consistent point", name, seen[name])
		}
	}
}

// TestNoInitialLoadLeavesExistingRowsAlone pins the default. Turning this on for
// every workflow would make an upgrade re-read every source table.
func TestNoInitialLoadLeavesExistingRowsAlone(t *testing.T) {
	f := newCDCFixture(t)
	ctx := t.Context()

	mustExec(t, f.db, fmt.Sprintf("INSERT INTO %s (name) VALUES ($1)", f.table), "pre-existing")

	f.start(t, ctx, false) // the ordinary path, no initial load

	time.Sleep(3 * time.Second)
	mustExec(t, f.db, fmt.Sprintf("INSERT INTO %s (name) VALUES ($1)", f.table), "streamed")

	seen := collectNames(t, f, map[string]bool{"streamed": true}, 60*time.Second)

	if seen["streamed"] == 0 {
		t.Fatal("the streamed row never arrived, so this proves nothing about the backfill")
	}
	if seen["pre-existing"] > 0 {
		t.Error("a row that predates the workflow arrived without an initial load being " +
			"asked for; enabling this by default would re-read every source table on upgrade")
	}
}
