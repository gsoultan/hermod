//go:build integration
// +build integration

package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"
)

// A MySQL JSON column against a real server, on both paths.
//
// The binlog path decodes go-mysql's row images; the Sample path runs a plain
// SELECT through database/sql. Both handed the column over as a string holding
// JSON, so a transformation or sink that wanted meta.addr.city got nothing --
// and, unlike PostgreSQL, both paths were wrong, so nothing in the product
// contradicted itself loudly enough to be noticed.
//
// Needs a server with ROW binlog:
//
//	container run -d --name hermod-jsonb-mysql -p 3316:3306 \
//	  -e MYSQL_ROOT_PASSWORD=root -e MYSQL_DATABASE=hermod_it \
//	  docker.io/arm64v8/mysql:8 \
//	  --binlog-format=ROW --binlog-row-image=FULL --server-id=1 --log-bin=mysql-bin
//
//	HERMOD_INTEGRATION=1 \
//	MYSQL_DSN='root:root@tcp(127.0.0.1:3316)/hermod_it?parseTime=true' \
//	  go test -tags=integration -run TestMySQLJSON ./pkg/comm/source/mysql/
func TestMySQLJSONColumnIsAnObjectOnBothPaths(t *testing.T) {
	dsn := os.Getenv("MYSQL_DSN")
	if dsn == "" || os.Getenv("HERMOD_INTEGRATION") != "1" {
		t.Skip("integration: set HERMOD_INTEGRATION=1 and MYSQL_DSN to run")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if err := db.PingContext(t.Context()); err != nil {
		t.Fatalf("ping: %v", err)
	}

	table := fmt.Sprintf("json_%d", time.Now().UnixNano())
	if _, err := db.ExecContext(t.Context(), fmt.Sprintf(
		`CREATE TABLE %s (id INT PRIMARY KEY, meta JSON, note VARCHAR(128))`, table)); err != nil {
		t.Fatalf("create %s: %v", table, err)
	}
	t.Cleanup(func() { _, _ = db.Exec("DROP TABLE IF EXISTS " + table) })

	const doc = `{"tier":"gold","addr":{"city":"Jakarta","zip":"12345"},"tags":["a","b"]}`
	// note is a VARCHAR that happens to hold JSON: it must stay a string, or the
	// fix has started guessing from content rather than from the column type.
	const note = `{"looks":"like json"}`

	src := NewMySQLSource(dsn, true)
	t.Cleanup(func() { _ = src.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	if err := src.init(ctx); err != nil {
		t.Fatalf("init: %v", err)
	}
	time.Sleep(2 * time.Second) // canal registers asynchronously

	if _, err := db.ExecContext(ctx,
		fmt.Sprintf("INSERT INTO %s (id, meta, note) VALUES (?, CAST(? AS JSON), ?)", table),
		1, doc, note); err != nil {
		t.Fatalf("insert: %v", err)
	}

	readCtx, readCancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer readCancel()
	rows := readOurRows(readCtx, src, table, 1)
	if len(rows) == 0 {
		t.Fatal("the source delivered nothing for its own table")
	}
	cdc := rows[0]
	t.Logf("CDC    meta is %T", cdc["meta"])

	cdcObj, ok := cdc["meta"].(map[string]any)
	if !ok {
		t.Fatalf("CDC meta is %T, want map[string]any", cdc["meta"])
	}
	if addr, _ := cdcObj["addr"].(map[string]any); addr["city"] != "Jakarta" {
		t.Errorf("CDC meta.addr.city = %#v, want Jakarta", addr["city"])
	}
	if cdc["note"] != note {
		t.Errorf("CDC note = %#v (%T), want the raw string -- a VARCHAR holding "+
			"JSON is still a VARCHAR", cdc["note"], cdc["note"])
	}

	// Sample: the call the workflow editor builds its field list from.
	sampleMsg, err := src.Sample(ctx, table)
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}
	var sample map[string]any
	if err := json.Unmarshal(sampleMsg.After(), &sample); err != nil {
		t.Fatalf("decoding sample: %v", err)
	}
	t.Logf("Sample meta is %T", sample["meta"])

	sampleObj, ok := sample["meta"].(map[string]any)
	if !ok {
		t.Fatalf("Sample meta is %T, want map[string]any", sample["meta"])
	}
	if sample["note"] != note {
		t.Errorf("Sample note = %#v, want the raw string", sample["note"])
	}

	// The two paths must agree, which is the whole point.
	if !reflect.DeepEqual(cdcObj, sampleObj) {
		t.Errorf("the two paths disagree:\n  CDC    %#v\n  Sample %#v", cdcObj, sampleObj)
	}
}
