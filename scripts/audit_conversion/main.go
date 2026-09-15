// Command audit_conversion reports the data_conversion nodes whose behaviour
// changed when an unresolvable field stopped being a silent no-op.
//
// Before that change a field name that resolved to nothing returned the message
// unchanged, with no error. It is now a conversion failure following the node's
// Error Behaviour, and the editor defaults that to "fail" -- so a node that has
// been quietly doing nothing starts failing its workflow.
//
// This finds those nodes. It cannot prove a field resolves at runtime, because
// that depends on the live message, so it uses the best evidence in storage: the
// source's stored Sample, which is the same thing the editor builds its field
// list from. A field absent from the Sample is the shape that was silently doing
// nothing.
//
//	go run ./scripts/audit_conversion -dsn '/path/to/hermod.db'
//	go run ./scripts/audit_conversion -driver pgx -dsn 'postgres://...'
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

type node struct {
	ID     string         `json:"id"`
	Type   string         `json:"type"`
	Config map[string]any `json:"config"`
}

type finding struct {
	workflow string
	id       string
	active   bool
	node     string
	field    string
	behavior string
	verdict  string
	atRisk   bool
}

func main() {
	driver := flag.String("driver", "sqlite", "sqlite or pgx")
	dsn := flag.String("dsn", "", "database path or connection string")
	activeOnly := flag.Bool("active-only", false, "only report workflows that are active")
	flag.Parse()

	atRisk, err := run(context.Background(), *driver, *dsn, *activeOnly)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	if atRisk > 0 {
		// Non-zero so this can gate a rollout.
		os.Exit(1)
	}
}

func run(ctx context.Context, driver, dsn string, activeOnly bool) (int, error) {
	if dsn == "" {
		return 0, errors.New("-dsn is required")
	}

	db, err := sql.Open(driver, dsn)
	if err != nil {
		return 0, fmt.Errorf("open: %w", err)
	}
	defer func() { _ = db.Close() }()

	samples, err := sourceSamples(ctx, db)
	if err != nil {
		return 0, err
	}

	findings, err := auditWorkflows(ctx, db, samples, activeOnly)
	if err != nil {
		return 0, err
	}

	atRisk := 0
	for _, f := range findings {
		if f.atRisk {
			atRisk++
			fmt.Printf("AT RISK  %s (%s) active=%v node=%s field=%q errorBehavior=%q\n         %s\n",
				f.workflow, f.id, f.active, f.node, f.field, f.behavior, f.verdict)
			continue
		}
		fmt.Printf("ok       %s (%s) node=%s field=%q errorBehavior=%q -- %s\n",
			f.workflow, f.id, f.node, f.field, f.behavior, f.verdict)
	}

	fmt.Printf("\n%d data_conversion node(s) reviewed, %d at risk.\n", len(findings), atRisk)
	if atRisk > 0 {
		fmt.Println(`Set errorBehavior to "keep" on any whose field is legitimately absent ` +
			"on some messages, or correct the field name.")
	}
	return atRisk, nil
}

// sourceSamples returns, per source id, the dotted field paths its stored sample
// offers.
func sourceSamples(ctx context.Context, db *sql.DB) (map[string]map[string]bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, COALESCE(sample, '') FROM sources`)
	if err != nil {
		return nil, fmt.Errorf("read sources: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := map[string]map[string]bool{}
	for rows.Next() {
		var id, sample string
		if err := rows.Scan(&id, &sample); err != nil {
			return nil, fmt.Errorf("scan source: %w", err)
		}
		out[id] = flatten(sample)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sources: %w", err)
	}
	return out, nil
}

func auditWorkflows(ctx context.Context, db *sql.DB, samples map[string]map[string]bool, activeOnly bool) ([]finding, error) {
	q := `SELECT id, name, COALESCE(active, false), COALESCE(nodes, '[]') FROM workflows`
	if activeOnly {
		q += ` WHERE active`
	}
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("read workflows: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var findings []finding
	for rows.Next() {
		var id, name, raw string
		var active bool
		if err := rows.Scan(&id, &name, &active, &raw); err != nil {
			return nil, fmt.Errorf("scan workflow: %w", err)
		}
		var nodes []node
		if err := json.Unmarshal([]byte(raw), &nodes); err != nil {
			fmt.Printf("! %s (%s): nodes did not parse: %v\n", name, id, err)
			continue
		}
		findings = append(findings, auditNodes(name, id, active, nodes, samples)...)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate workflows: %w", err)
	}
	return findings, nil
}

func auditNodes(name, id string, active bool, nodes []node, samples map[string]map[string]bool) []finding {
	known := knownFields(nodes, samples)

	var out []finding
	for _, n := range nodes {
		if tt, _ := n.Config["transType"].(string); tt != "data_conversion" {
			continue
		}
		field, _ := n.Config["field"].(string)
		behavior, _ := n.Config["errorBehavior"].(string)
		if behavior == "" {
			behavior = "fail (unset; the editor's default)"
		}

		verdict, risky := assess(field, known)
		// "keep" is the documented way to say a field is legitimately optional.
		if strings.HasPrefix(strings.ToLower(behavior), "keep") {
			risky = false
		}
		out = append(out, finding{
			workflow: name, id: id, active: active, node: n.ID,
			field: field, behavior: behavior, verdict: verdict, atRisk: risky,
		})
	}
	return out
}

// knownFields merges the sample fields of every source this workflow refers to:
// a node's field may come from any of them.
func knownFields(nodes []node, samples map[string]map[string]bool) map[string]bool {
	known := map[string]bool{}
	for _, n := range nodes {
		for _, key := range []string{"sourceId", "refId", "RefID"} {
			sid, _ := n.Config[key].(string)
			if sid == "" {
				continue
			}
			for f := range samples[sid] {
				known[f] = true
			}
		}
	}
	return known
}

func assess(field string, known map[string]bool) (verdict string, risky bool) {
	switch {
	case field == "":
		return "no field configured: the node has always been a no-op and still is", false
	case len(known) == 0:
		return "no source sample stored, so this cannot be checked from storage", true
	case !known[field]:
		return "FIELD NOT IN SOURCE SAMPLE", true
	}
	return "field is in the source sample", false
}

// flatten returns the dotted paths a sample document offers, the same way the
// editor's field list recurses into objects.
func flatten(sample string) map[string]bool {
	out := map[string]bool{}
	var doc any
	if err := json.Unmarshal([]byte(sample), &doc); err != nil {
		return out
	}
	var walk func(prefix string, v any)
	walk = func(prefix string, v any) {
		m, ok := v.(map[string]any)
		if !ok {
			return
		}
		for k, child := range m {
			path := k
			if prefix != "" {
				path = prefix + "." + k
			}
			out[path] = true
			walk(path, child)
		}
	}
	walk("", doc)
	return out
}
