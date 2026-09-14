//go:build integration
// +build integration

package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
)

// What shape does a jsonb column have by the time the pipeline sees it?
//
// The answer differs by path, and nothing in the code says so. The snapshot,
// polling and Sample paths go through pgx's rows.Values(), where the registered
// jsonb codec unmarshals into map[string]any -- so the [] byte -> string(b)
// branch at postgres.go:1406/2608/2781/2858 is never reached and the object
// survives. The live CDC path decodes pgoutput tuples by hand
// (postgres.go:1211-1218), and pgoutput sends every column as text because
// START_REPLICATION asks for proto_version '1' with no binary option
// (postgres.go:1750-1751) -- so the same column arrives as a Go string holding
// JSON.
//
// That matters because the workflow editor builds its "available fields" list
// from the source's stored Sample (useNodeContext.ts:56-64) and recurses into
// objects only (transformationUtils.ts:18-33). The editor therefore offers
// meta.addr.city on a path where the running pipeline has meta as an opaque
// string.
//
// These tests pin the current behaviour against a real server so a fix has
// something to flip, and so the asymmetry stops being folklore.
//
// The server must run with wal_level=logical. A dedicated one, so the tests do
// not fight the dev database for port 5432:
//
//	HERMOD_DEV_PG_CONTAINER=hermod-jsonb-pg HERMOD_DEV_PG_PORT=5443 \
//	  ./scripts/create-postgres.sh
//	HERMOD_INTEGRATION=1 \
//	POSTGRES_DSN='postgres://postgres:postgres@localhost:5443/hermod_test_source?sslmode=disable' \
//	  go test -tags=integration -run TestJSONB ./pkg/comm/source/postgres/
func TestJSONBShape(t *testing.T) {
	f := newCDCFixture(t)
	mustExec(t, f.db, fmt.Sprintf("ALTER TABLE %s ADD COLUMN meta jsonb", f.table))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	f.start(t, ctx, false)

	const doc = `{"tier":"gold","addr":{"city":"Jakarta","zip":"12345"},"tags":["a","b"]}`
	mustExec(t, f.db,
		fmt.Sprintf("INSERT INTO %s (name, email, meta) VALUES ($1, $2, $3::jsonb)", f.table),
		"Ada", "ada@example.com", doc)

	msgs, ok := f.collectUntil(t, 30*time.Second, func(m hermod.Message) bool {
		return m.Operation() == hermod.OpCreate
	})
	if !ok {
		t.Fatalf("no insert arrived on the CDC stream; %s", f.why())
	}
	var after map[string]any
	for _, m := range msgs {
		if m.Operation() == hermod.OpCreate {
			after = decode(t, m.After())
		}
	}
	if after == nil {
		t.Fatalf("insert carried no after-image; %s", f.why())
	}

	cdcMeta, hasMeta := after["meta"]
	if !hasMeta {
		t.Fatalf("after-image has no meta column: %#v", after)
	}
	t.Logf("CDC    meta is %T -> %#v", cdcMeta, cdcMeta)

	// Same row, read back through Sample -- the call the UI's field list uses.
	sampleMsg, err := f.source.Sample(ctx, f.table)
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}
	sampleAfter := decode(t, sampleMsg.After())
	sampleMeta := sampleAfter["meta"]
	t.Logf("Sample meta is %T -> %#v", sampleMeta, sampleMeta)

	// Both paths must now agree, and both must be the object.
	cdcObj, isObj := cdcMeta.(map[string]any)
	if !isObj {
		t.Fatalf("CDC meta is %T, want map[string]any -- the editor's field "+
			"list is built from Sample, so CDC has to produce the same shape", cdcMeta)
	}
	sampleObj, isObj := sampleMeta.(map[string]any)
	if !isObj {
		t.Fatalf("Sample meta is %T, want map[string]any", sampleMeta)
	}
	if !reflect.DeepEqual(cdcObj, sampleObj) {
		t.Errorf("the two paths disagree:\n  CDC    %#v\n  Sample %#v", cdcObj, sampleObj)
	}

	// The point of all of it: a nested path resolves.
	for label, obj := range map[string]map[string]any{"CDC": cdcObj, "Sample": sampleObj} {
		addr, _ := obj["addr"].(map[string]any)
		if addr["city"] != "Jakarta" {
			t.Errorf("%s meta.addr.city = %#v, want Jakarta", label, addr["city"])
		}
		if tags, _ := obj["tags"].([]any); len(tags) != 2 {
			t.Errorf("%s meta.tags = %#v, want two elements", label, obj["tags"])
		}
	}

	// And the whole message still round-trips, which is what the sink writes.
	for _, m := range msgs {
		if m.Operation() != hermod.OpCreate {
			continue
		}
		if _, err := json.Marshal(m.ToMap()); err != nil {
			t.Errorf("message does not marshal: %v", err)
		}
	}
}

// An UPDATE that does not touch a TOASTed jsonb column sends 'u' (unchanged
// TOASTed value, pglogrepl/message.go:362) for it. None of the four tuple
// decoders in postgres.go handle 'u' -- the switch covers 'n', 't' and 'b' --
// so the column is not written to the map at all and drops out of the
// after-image. jsonb TOASTs readily, so this is the ordinary case for any
// document over a couple of kilobytes.
func TestJSONBToastUnchanged(t *testing.T) {
	f := newCDCFixture(t)
	mustExec(t, f.db, fmt.Sprintf("ALTER TABLE %s ADD COLUMN meta jsonb", f.table))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	f.start(t, ctx, false)

	// Incompressible enough that PostgreSQL stores it out of line rather than
	// just compressing it inline: a compressible blob stays in the main tuple
	// and is sent in full, which would make this test pass for the wrong reason.
	big := randomishHex(16000)
	doc := fmt.Sprintf(`{"blob":%q,"tier":"gold"}`, big)
	mustExec(t, f.db,
		fmt.Sprintf("INSERT INTO %s (name, email, meta) VALUES ($1, $2, $3::jsonb)", f.table),
		"Ada", "ada@example.com", doc)

	// Prove the value really is out of line before drawing any conclusion from
	// its absence: a value that stayed in the main tuple is sent in full, and
	// this test would then pass for a reason that has nothing to do with TOAST.
	toastBytes := scalar(t, f.db, fmt.Sprintf(
		"SELECT pg_relation_size(reltoastrelid) FROM pg_class WHERE relname = '%s'", f.table))
	t.Logf("TOAST relation for %s holds %s bytes", f.table, toastBytes)
	if toastBytes == "" || toastBytes == "0" {
		t.Fatalf("meta was not TOASTed (toast relation is %q bytes); "+
			"this test cannot measure the 'u' case -- make the document larger "+
			"or less compressible", toastBytes)
	}

	if _, ok := f.collectUntil(t, 30*time.Second, func(m hermod.Message) bool {
		return m.Operation() == hermod.OpCreate
	}); !ok {
		t.Fatalf("no insert arrived; %s", f.why())
	}

	// Touch a different column. meta is unchanged and TOASTed.
	mustExec(t, f.db, fmt.Sprintf("UPDATE %s SET email = $1 WHERE name = $2", f.table),
		"ada2@example.com", "Ada")

	msgs, ok := f.collectUntil(t, 30*time.Second, func(m hermod.Message) bool {
		return m.Operation() == hermod.OpUpdate
	})
	if !ok {
		t.Fatalf("no update arrived; %s", f.why())
	}
	var after map[string]any
	for _, m := range msgs {
		if m.Operation() == hermod.OpUpdate {
			after = decode(t, m.After())
		}
	}
	if after == nil {
		t.Fatalf("update carried no after-image; %s", f.why())
	}

	keys := make([]string, 0, len(after))
	for k := range after {
		keys = append(keys, k)
	}
	t.Logf("UPDATE after-image columns: %v", keys)
	if v, present := after["meta"]; present {
		s, _ := v.(string)
		t.Logf("meta IS present, %T, len=%d", v, len(s))
	} else {
		t.Logf("meta is ABSENT from the after-image (unchanged TOASTed value dropped)")
	}

	if _, present := after["email"]; !present {
		t.Fatalf("the column that did change is missing too; the test is not measuring TOAST: %#v", keys)
	}

	// REPLICA IDENTITY FULL (set by newCDCFixture) puts the untouched value in
	// the before-image, so the after-image can be completed from it.
	got, present := after["meta"]
	if !present {
		t.Fatalf("meta is missing from the after-image of an UPDATE that did not "+
			"touch it; the before-image carries the value under REPLICA IDENTITY "+
			"FULL and it should have been recovered (columns: %v)", keys)
	}
	obj, isObj := got.(map[string]any)
	if !isObj {
		t.Fatalf("recovered meta is %T, want map[string]any", got)
	}
	if obj["tier"] != "gold" {
		t.Errorf("meta.tier = %#v, want gold", obj["tier"])
	}
	if blob, _ := obj["blob"].(string); len(blob) != len(big) {
		t.Errorf("meta.blob is %d chars, want the full %d -- the recovered value "+
			"was truncated", len(blob), len(big))
	}
}

// With REPLICA IDENTITY DEFAULT and no key change, PostgreSQL sends no
// before-image at all, so an unchanged TOASTed column is genuinely not in the
// WAL record. The column cannot be recovered and must not be invented -- but
// its absence has to be announced, or a sink cannot tell "not sent" from "not a
// column" and writes the row without it.
func TestJSONBToastUnrecoverable(t *testing.T) {
	f := newCDCFixture(t)
	mustExec(t, f.db, fmt.Sprintf("ALTER TABLE %s ADD COLUMN meta jsonb", f.table))
	mustExec(t, f.db, fmt.Sprintf("ALTER TABLE %s REPLICA IDENTITY DEFAULT", f.table))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	f.start(t, ctx, false)

	big := randomishHex(16000)
	mustExec(t, f.db,
		fmt.Sprintf("INSERT INTO %s (name, email, meta) VALUES ($1, $2, $3::jsonb)", f.table),
		"Ada", "ada@example.com", fmt.Sprintf(`{"blob":%q}`, big))
	if _, ok := f.collectUntil(t, 30*time.Second, func(m hermod.Message) bool {
		return m.Operation() == hermod.OpCreate
	}); !ok {
		t.Fatalf("no insert arrived; %s", f.why())
	}

	mustExec(t, f.db, fmt.Sprintf("UPDATE %s SET email = $1 WHERE name = $2", f.table),
		"ada2@example.com", "Ada")

	msgs, ok := f.collectUntil(t, 30*time.Second, func(m hermod.Message) bool {
		return m.Operation() == hermod.OpUpdate
	})
	if !ok {
		t.Fatalf("no update arrived; %s", f.why())
	}
	var upd hermod.Message
	for _, m := range msgs {
		if m.Operation() == hermod.OpUpdate {
			upd = m
		}
	}
	if upd == nil {
		t.Fatal("no update message")
	}
	after := decode(t, upd.After())
	if _, present := after["meta"]; present {
		t.Errorf("meta = %#v; nothing in the WAL record carried it, so it must "+
			"not appear in the after-image", after["meta"])
	}
	if got := upd.Metadata()["unchanged_toast_columns"]; got != "meta" {
		t.Errorf("unchanged_toast_columns = %q, want \"meta\" -- an omitted "+
			"column has to be announced (metadata: %#v)", got, upd.Metadata())
	}
}

func randomishHex(n int) string {
	var b strings.Builder
	b.Grow(n)
	// A linear congruential sequence: deterministic for reproducibility, but
	// high-entropy enough that pglz cannot shrink it below the TOAST threshold.
	x := uint64(0x2545F4914F6CDD1D)
	const hexdigits = "0123456789abcdef"
	for i := 0; i < n; i++ {
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
		b.WriteByte(hexdigits[x&0xF])
	}
	return b.String()
}
