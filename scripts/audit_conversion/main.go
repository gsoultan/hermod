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
	"database/sql"
	"encoding/json"
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

func main() {
	driver := flag.String("driver", "sqlite", "sqlite or pgx")
	dsn := flag.String("dsn", "", "database path or connection string")
	activeOnly := flag.Bool("active-only", false, "only report workflows that are active")
	flag.Parse()

	if *dsn == "" {
		fmt.Fprintln(os.Stderr, "-dsn is required")
		os.Exit(2)
	}

	db, err := sql.Open(*driver, *dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = db.Close() }()

	// Every source's sample, so a node's field can be checked against the shape
	// its workflow actually reads.
	sampleFields := map[string]map[string]bool{}
	srcRows, err := db.Query(`SELECT id, COALESCE(sample, '') FROM sources`)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read sources: %v\n", err)
		os.Exit(1)
	}
	for srcRows.Next() {
		var id, sample string
		if err := srcRows.Scan(&id, &sample); err != nil {
			fmt.Fprintf(os.Stderr, "scan source: %v\n", err)
			os.Exit(1)
		}
		sampleFields[id] = flatten(sample)
	}
	_ = srcRows.Close()

	q := `SELECT id, name, COALESCE(active, false), COALESCE(nodes, '[]') FROM workflows`
	if *activeOnly {
		q += ` WHERE active`
	}
	rows, err := db.Query(q)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read workflows: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = rows.Close() }()

	var atRisk, reviewed int
	for rows.Next() {
		var id, name, raw string
		var active bool
		if err := rows.Scan(&id, &name, &active, &raw); err != nil {
			fmt.Fprintf(os.Stderr, "scan workflow: %v\n", err)
			os.Exit(1)
		}
		var nodes []node
		if err := json.Unmarshal([]byte(raw), &nodes); err != nil {
			fmt.Printf("! %s (%s): nodes did not parse: %v\n", name, id, err)
			continue
		}

		// Whatever sources this workflow reads, merged: a node's field may come
		// from any of them.
		known := map[string]bool{}
		for _, n := range nodes {
			for _, key := range []string{"sourceId", "refId", "RefID"} {
				if sid, _ := n.Config[key].(string); sid != "" {
					for f := range sampleFields[sid] {
						known[f] = true
					}
				}
			}
		}

		for _, n := range nodes {
			if tt, _ := n.Config["transType"].(string); tt != "data_conversion" {
				continue
			}
			reviewed++
			field, _ := n.Config["field"].(string)
			behaviour, _ := n.Config["errorBehavior"].(string)
			if behaviour == "" {
				behaviour = "fail (unset; the editor's default)"
			}

			verdict := "field is in the source sample"
			risky := false
			switch {
			case field == "":
				verdict = "no field configured: the node has always been a no-op and still is"
			case len(known) == 0:
				verdict = "no source sample stored, so this cannot be checked from storage"
				risky = true
			case !known[field]:
				verdict = "FIELD NOT IN SOURCE SAMPLE"
				risky = true
			}
			if risky && !strings.HasPrefix(strings.ToLower(behaviour), "keep") {
				atRisk++
				fmt.Printf("AT RISK  %s (%s) active=%v node=%s field=%q errorBehavior=%q\n         %s\n",
					name, id, active, n.ID, field, behaviour, verdict)
			} else {
				fmt.Printf("ok       %s (%s) node=%s field=%q errorBehavior=%q -- %s\n",
					name, id, n.ID, field, behaviour, verdict)
			}
		}
	}
	if err := rows.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "iterate workflows: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\n%d data_conversion node(s) reviewed, %d at risk.\n", reviewed, atRisk)
	if atRisk > 0 {
		fmt.Println("Set errorBehavior to \"keep\" on any whose field is legitimately absent " +
			"on some messages, or correct the field name.")
		os.Exit(1)
	}
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
