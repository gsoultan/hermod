package sql

// What message_trace_steps costs per step, measured through the real driver
// into a file-backed SQLite database rather than estimated from the DDL.
//
// A hand-written schema estimate is worth nothing on this table: the SQLite
// driver stores time.Time as 36-byte text, which a by-hand sum misses by ~20%.
// dashboard_history was measured the same way for the same reason.
//
// The population is a realistic nine-step workflow. The numbers below were
// taken at 20,000 traces / 180,000 steps; this gate runs a tenth of that so it
// stays in the ordinary suite, and the ratios it asserts do not depend on size.
//
//	                        before      after
//	SQLite, whole database  171.33 MB   80.20 MB   (2.14x)
//	  bytes per step          998.1      467.2
//	  payload bytes on disk    94.85 MB  20.45 MB   (4.64x)
//	PostgreSQL 18, message_trace_steps
//	                        135.20 MB   59.45 MB   (2.27x)
//	  bytes per step            787.6      346.3

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	_ "modernc.org/sqlite"
)

const tracesInProbe = 2000

// realisticPayload is what a CDC row actually looks like on the wire: an
// envelope around a table row of ~15 columns. Values vary per message so
// nothing compresses for free across rows — only the structure repeats, which
// is the real-world case and the pessimistic one.
func realisticPayload(seq int, stage string) map[string]any {
	row := map[string]any{
		"order_id":        fmt.Sprintf("ord_%012d", seq),
		"customer_id":     fmt.Sprintf("cus_%09d", seq%100000),
		"customer_email":  fmt.Sprintf("user%d@example.com", seq%50000),
		"status":          []string{"pending", "paid", "shipped", "cancelled"}[seq%4],
		"currency":        "USD",
		"total_amount":    float64(seq%100000) / 100.0,
		"tax_amount":      float64(seq%9000) / 100.0,
		"shipping_amount": float64(seq%1500) / 100.0,
		"item_count":      seq%12 + 1,
		"warehouse_code":  fmt.Sprintf("WH-%03d", seq%40),
		"created_at":      time.Unix(1700000000+int64(seq), 0).UTC().Format(time.RFC3339),
		"updated_at":      time.Unix(1700000500+int64(seq), 0).UTC().Format(time.RFC3339),
		"notes":           fmt.Sprintf("auto-generated order %d for the footprint gate", seq),
		"is_gift":         seq%7 == 0,
		"channel":         []string{"web", "mobile", "pos", "api"}[seq%4],
	}
	if stage != "" {
		row["_stage"] = stage
	}
	return map[string]any{
		"op":     "u",
		"table":  "public.orders",
		"lsn":    fmt.Sprintf("0/%X", 0x16B2C40+seq*72),
		"ts_ms":  1700000000000 + int64(seq)*37,
		"source": map[string]any{"connector": "postgres", "db": "shop", "schema": "public"},
		"after":  row,
	}
}

// traceShape is the step sequence a two-transformation workflow actually
// produces, read off the write call sites:
//
//	runner.go            workflow_start, validator, router
//	registry_broadcast   the source ingest step
//	traversal            one step per node, under node.ID
//	registry.go          one step per transformation, under its transType
//	writer.go            one step per sink
//
// Three distinct payloads across nine steps. That ratio is the point: only the
// transformations change the message, and one of them is recorded twice.
type probeStep struct {
	nodeID  string
	payload string // "" is the untransformed input
}

var traceShape = []probeStep{
	{"workflow_start", ""},
	{"src_postgres_orders", ""},
	{"validator", ""},
	{"node_a1b2c3d4", "t1"},
	{"field_mapper", "t1"},
	{"node_e5f6a7b8", "t2"},
	{"data_conversion", "t2"},
	{"router", ""},
	{"sink_warehouse", "t2"},
}

func openFootprintDB(t *testing.T, path string) (storage.Storage, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.SetMaxOpenConns(1)
	s := NewSQLStorage(db, "sqlite")
	if err := s.(interface{ Init(context.Context) error }).Init(context.Background()); err != nil {
		t.Fatalf("init: %v", err)
	}
	return s, db
}

func TestMessageTraceStepsFootprint(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/footprint.db"
	s, db := openFootprintDB(t, path)

	ctx := context.Background()
	base := time.Now().UTC().Add(-24 * time.Hour)

	var rawAfterBytes, distinctPayloads int64
	for i := range tracesInProbe {
		msgID := fmt.Sprintf("msg_%s-%012d", "3f9a2c1d-77b4-4e0a-8f21", i)
		ts := base.Add(time.Duration(i) * 4 * time.Millisecond)

		seen := map[[32]byte]bool{}
		for j, st := range traceShape {
			payload := realisticPayload(i, st.payload)
			b, _ := json.Marshal(payload)
			rawAfterBytes += int64(len(b))
			if h := sha256.Sum256(b); !seen[h] {
				distinctPayloads++
				seen[h] = true
			}

			if err := s.RecordTraceStep(ctx, "wf_probe", msgID, hermod.TraceStep{
				NodeID:    st.nodeID,
				Timestamp: ts.Add(time.Duration(j) * 900 * time.Microsecond),
				Duration:  time.Duration(400+j*130) * time.Microsecond,
				After:     payload,
			}); err != nil {
				t.Fatalf("record step %d/%d: %v", i, j, err)
			}
		}
	}

	var carriers, storedBytes int64
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(LENGTH(after_blob)),0)
		 FROM message_trace_steps WHERE after_blob IS NOT NULL`).Scan(&carriers, &storedBytes); err != nil {
		t.Fatalf("count carriers: %v", err)
	}
	var legacy int64
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM message_trace_steps WHERE after_data IS NOT NULL`).Scan(&legacy); err != nil {
		t.Fatalf("count legacy: %v", err)
	}

	_, _ = db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
	_, _ = db.ExecContext(ctx, "VACUUM")
	_ = db.Close()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	steps := int64(tracesInProbe) * int64(len(traceShape))
	t.Logf("steps %d · database %.2f MB · %.1f B/step · payload %.2f MB of %.2f MB raw (%.2fx) · carriers %d/%d",
		steps, float64(fi.Size())/(1<<20), float64(fi.Size())/float64(steps),
		float64(storedBytes)/(1<<20), float64(rawAfterBytes)/(1<<20),
		float64(rawAfterBytes)/float64(storedBytes), carriers, steps)

	// One copy of a payload per message, and no more. This shape has three
	// distinct payloads across nine steps, so a higher count means a duplicate
	// is being stored again — the regression this change exists to prevent.
	if carriers != distinctPayloads {
		t.Errorf("stored %d payload copies for %d distinct payloads: dedup is not holding",
			carriers, distinctPayloads)
	}
	// Nothing writes the legacy plain-JSON column any more; it is read-only,
	// kept so rows written before this change still resolve without a backfill.
	if legacy != 0 {
		t.Errorf("%d rows wrote after_data; new rows belong in after_blob", legacy)
	}
	// Measured 4.64x. A floor of 3x leaves room for zstd version drift without
	// letting a silent fallback to uncompressed storage through.
	if ratio := float64(rawAfterBytes) / float64(storedBytes); ratio < 3.0 {
		t.Errorf("payload bytes on disk are only %.2fx smaller than the raw JSON, want at least 3x", ratio)
	}
	// Measured 467.2 B/step at 20k traces. The ceiling has headroom for page
	// slack at this smaller population; it is here to catch a column or an
	// index coming back, not to pin a number.
	if perStep := float64(fi.Size()) / float64(steps); perStep > 700 {
		t.Errorf("%.1f bytes per step, ceiling is 700 (was 998.1 before dedup and compression)", perStep)
	}
}
