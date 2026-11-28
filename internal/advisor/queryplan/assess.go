// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package queryplan

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Severities, best first.
const (
	SeverityOK   = "ok"
	SeverityInfo = "info"
	SeverityWarn = "warn"
)

// Rules a finding can come from.
const (
	RuleIndexUsed       = "index_used"
	RuleSmallTable      = "small_table"
	RuleIndexNotChosen  = "index_not_chosen"
	RuleNoFilter        = "no_filter"
	RuleMissingIndex    = "missing_index"
	RuleWrappedColumn   = "wrapped_column"
	RuleLeadingWildcard = "leading_wildcard"
	RuleWrongIndexType  = "wrong_index_type"
	RuleJSONB           = "jsonb_index"
	RuleUnknownTable    = "unknown_table"
)

// SmallTableRows is the size below which a full scan is as good as an index.
const SmallTableRows = 10_000

// Index is one index of a table, as the catalog describes it.
type Index struct {
	Name string `json:"name"`
	// Method is the access method: btree, hash, gin, gist, brin, ...
	Method string `json:"method"`
	// Keys are the key columns or expressions, as PostgreSQL prints them.
	Keys       []string `json:"keys"`
	Unique     bool     `json:"unique,omitempty"`
	Definition string   `json:"definition,omitempty"`
}

// Table is a table a plan reads, with its size and indexes.
type Table struct {
	Schema string `json:"schema"`
	Name   string `json:"name"`
	// Rows is the catalog's estimate, or -1 when the table was never analyzed.
	Rows    float64 `json:"rows"`
	Indexes []Index `json:"indexes"`
	// Columns maps each column's name to its type, as format_type prints it.
	Columns map[string]string `json:"-"`
}

// ColumnType is the type of col as a query writes it, qualified or quoted,
// or "" when the table has no such column.
func (t Table) ColumnType(col string) string { return t.Columns[unquoteName(lastPart(col))] }

// Key is how a table is found in a catalog map: "schema.name", unquoted.
func (t Table) Key() string { return t.Schema + "." + t.Name }

// size says how many rows the table holds, for a sentence.
func (t Table) size() string {
	if t.Rows < 0 {
		return "rows (its size is unknown: it has never been analyzed)"
	}
	return fmt.Sprintf("about %.0f rows", t.Rows)
}

func (t Table) qualified() string { return quoteIdent(t.Schema) + "." + quoteIdent(t.Name) }

// Finding is one thing the check says about a lookup's query.
type Finding struct {
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	// Table is the table it is about, as "schema.name".
	Table  string `json:"table,omitempty"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	// Proposal is SQL a person can review and run. Hermod never runs it.
	Proposal string `json:"proposal,omitempty"`
	// Rewrite is a condition that means the same as the one the query has
	// and that an existing index can serve, to put in its place.
	Rewrite string `json:"rewrite,omitempty"`
}

// Assess reads each scan against its table's indexes.
func Assess(scans []Scan, tables map[string]Table) []Finding {
	var out []Finding
	for _, s := range scans {
		switch s.Node {
		case "Index Scan", "Index Only Scan", "Bitmap Index Scan":
			out = append(out, indexUsed(s, tables))
		case "Seq Scan":
			out = append(out, seqScan(s, tables)...)
		}
	}
	return out
}

func indexUsed(s Scan, tables map[string]Table) Finding {
	f := Finding{Rule: RuleIndexUsed, Severity: SeverityOK, Title: "Uses index " + s.Index}
	method, keys := "unknown", "?"
	for _, t := range tables {
		for _, ix := range t.Indexes {
			if ix.Name == s.Index {
				f.Table = t.Key()
				method, keys = ix.Method, strings.Join(ix.Keys, ", ")
			}
		}
	}
	how := "reads"
	if s.Node == "Index Only Scan" {
		how = "answers from the index alone, without reading"
	}
	where := f.Table
	if where == "" {
		where = "the table"
	}
	f.Detail = fmt.Sprintf("The query %s %s through %s, a %s index on (%s).", how, where, s.Index, method, keys)
	if len(s.Conds) > 0 {
		f.Detail += " Condition: " + s.Conds[0] + "."
	}
	return f
}

// predicate is one comparison on a scanned table's column or expression.
type predicate struct {
	alias string // the scan's alias, to write a rewrite as the query does
	expr  string // the scanned side, unqualified
	cols  []string
	op    string
	other string
	bare  bool // expr is a column, not an expression over one
}

var bareColumn = regexp.MustCompile(`^("(?:[^"]|"")+"|[A-Za-z_][\w$]*)$`)

func predicates(s Scan) []predicate {
	ref := aliasRef(s.Alias)
	var out []predicate
	seen := map[string]bool{}
	for _, c := range s.Conds {
		for _, term := range splitAnd(c) {
			lhs, op, rhs, ok := splitOp(term)
			if !ok {
				continue
			}
			l, r := ref.MatchString(lhs), ref.MatchString(rhs)
			if l == r {
				continue // both sides or neither: not a lookup on this table
			}
			if r {
				lhs, rhs = rhs, lhs
				op = flip(op)
			}
			p := predicate{alias: s.Alias, op: op, other: stripOuter(rhs)}
			for _, m := range ref.FindAllStringSubmatch(lhs, -1) {
				p.cols = append(p.cols, m[2])
			}
			p.expr = stripOuter(ref.ReplaceAllString(lhs, "$1$2"))
			p.bare = bareColumn.MatchString(p.expr)
			if k := p.expr + " " + p.op; !seen[k] {
				seen[k] = true
				out = append(out, p)
			}
		}
	}
	return out
}

func flip(op string) string {
	switch op {
	case "<":
		return ">"
	case ">":
		return "<"
	case "<=":
		return ">="
	case ">=":
		return "<="
	}
	return op
}

func isRange(op string) bool { return op == "<" || op == ">" || op == "<=" || op == ">=" }

func seqScan(s Scan, tables map[string]Table) []Finding {
	t, ok := tables[s.Schema+"."+s.Relation]
	if !ok {
		return []Finding{{Rule: RuleUnknownTable, Severity: SeverityInfo, Table: s.Schema + "." + s.Relation,
			Title:  "Full scan of " + s.Relation,
			Detail: "The query reads every row of " + s.Relation + ", and its indexes could not be read to say why."}}
	}
	if t.Rows >= 0 && t.Rows < SmallTableRows {
		return []Finding{{Rule: RuleSmallTable, Severity: SeverityOK, Table: t.Key(),
			Title:  "Full scan of a small table",
			Detail: fmt.Sprintf("%s holds about %.0f rows. Reading it whole is as fast as an index would be.", t.Key(), t.Rows)}}
	}
	preds := predicates(s)
	if len(preds) == 0 {
		return []Finding{{Rule: RuleNoFilter, Severity: SeverityWarn, Table: t.Key(),
			Title: "Reads all of " + t.Key(),
			Detail: fmt.Sprintf("The query reads all %s of %s with no condition an index could serve. "+
				"If the lookup needs only some of them, filter on an indexed column.", t.size(), t.Key())}}
	}
	c := classify(t, preds)
	out := c.findings
	if cols := slices.Concat(c.eq, c.rng); len(cols) > 0 {
		out = append(out, Finding{Rule: RuleMissingIndex, Severity: SeverityWarn, Table: t.Key(),
			Title: "No index for the lookup on " + t.Key(),
			Detail: fmt.Sprintf("The query reads all %s of %s to find matches on %s. "+
				"An index on these columns, equality first, lets it go straight to them.", t.size(), t.Key(), strings.Join(cols, ", ")),
			Proposal: createIndex(t, "btree", cols, "")})
	}
	if len(out) == 0 && len(c.covered) > 0 {
		out = append(out, Finding{Rule: RuleIndexNotChosen, Severity: SeverityInfo, Table: t.Key(),
			Title: "Index exists but the planner read the whole table",
			Detail: fmt.Sprintf("%s has %s for this condition, but the planner expects about %.0f of its %s to match, "+
				"so a full read is cheaper. That is right when the value is common; if it is rare, run ANALYZE on the table.",
				t.Key(), strings.Join(c.covered, ", "), s.Rows, t.size())})
	}
	return out
}

// classified is what a full scan's predicates call for.
type classified struct {
	findings []Finding
	// eq and rng are plain columns compared with = and with a range that no
	// index serves; covered names the indexes that could serve the rest.
	eq, rng, covered []string
}

func classify(t Table, preds []predicate) classified {
	var c classified
	for _, p := range preds {
		switch {
		case !p.bare:
			c.findings = append(c.findings, wrapped(t, p))
		case p.op == "~~" || p.op == "~~*":
			// LIKE with a leading wildcard, or any ILIKE: no btree serves it.
			if strings.HasPrefix(p.other, "'%") || p.op == "~~*" {
				c.findings = append(c.findings, wildcard(t, p))
			}
		case p.op == "@>" || p.op == "?" || p.op == "?|" || p.op == "?&":
			c.findings = append(c.findings, jsonbIndex(t, p))
		case p.op == "=":
			c.equality(t, p)
		case isRange(p.op):
			c.rangeOn(t, p)
		}
	}
	return c
}

func (c *classified) equality(t Table, p predicate) {
	if ix := leading(t, p.expr, "btree", "hash"); ix != nil {
		c.covered = append(c.covered, ix.Name)
	} else if !slices.Contains(c.eq, p.expr) {
		c.eq = append(c.eq, p.expr)
	}
}

func (c *classified) rangeOn(t Table, p predicate) {
	switch ix, hx := leading(t, p.expr, "btree", "brin"), leading(t, p.expr, "hash"); {
	case ix != nil:
		c.covered = append(c.covered, ix.Name)
	case hx != nil:
		c.findings = append(c.findings, wrongType(t, p, hx))
	case !slices.Contains(c.rng, p.expr):
		c.rng = append(c.rng, p.expr)
	}
}

// leading returns an index whose first key is expr and whose method is one of
// methods.
func leading(t Table, expr string, methods ...string) *Index {
	for i, ix := range t.Indexes {
		if len(ix.Keys) > 0 && sameExpr(ix.Keys[0], expr) && slices.Contains(methods, ix.Method) {
			return &t.Indexes[i]
		}
	}
	return nil
}

func sameExpr(a, b string) bool {
	norm := func(s string) string { return strings.Join(strings.Fields(stripOuter(s)), "") }
	return norm(a) == norm(b)
}

func wrapped(t Table, p predicate) Finding {
	f := Finding{Rule: RuleWrappedColumn, Severity: SeverityWarn, Table: t.Key(),
		Title: "A function on the column stops index use on " + t.Key()}
	if ix := leading(t, p.expr, "btree", "hash"); ix != nil {
		f.Rule, f.Severity = RuleIndexNotChosen, SeverityInfo
		f.Title = "Expression index exists but the planner read the whole table"
		f.Detail = fmt.Sprintf("%s has %s on %s, but the planner chose a full read. Run ANALYZE on the table so it can weigh the index.", t.Key(), ix.Name, p.expr)
		return f
	}
	var defeated []string
	for _, c := range p.cols {
		if ix := leading(t, c, "btree", "hash"); ix != nil {
			defeated = append(defeated, ix.Name+" on "+c)
		}
	}
	f.Detail = fmt.Sprintf("The query compares %s, not the column itself, so it reads every row of %s and computes it for each.", p.expr, t.Key())
	if len(defeated) > 0 {
		f.Detail += " The index " + strings.Join(defeated, ", ") + " cannot be used for that."
	}
	if rw := truncRange(t, p); rw != "" && len(defeated) > 0 {
		f.Rewrite = rw
		f.Detail += " Comparing a range of the column instead gives the same rows and uses that index, with no new index needed."
		return f
	}
	f.Detail += " Either compare the plain column (clean the values once, when they are written), or index the expression exactly as the query writes it."
	f.Proposal = createIndex(t, "btree", []string{"(" + p.expr + ")"}, "")
	return f
}

// truncExpr is date_trunc('<unit>'::text, <column>) as PostgreSQL prints it.
var truncExpr = regexp.MustCompile(`^date_trunc\('(microseconds|milliseconds|second|minute|hour|day|week|month|quarter|year)'::text, ("(?:[^"]|"")+"|[A-Za-z_][\w$]*)\)$`)

// truncRange rewrites date_trunc('unit', col) = v as the range of col that
// truncates to v: col >= v AND col < v + interval '1 unit'. Both select the
// same rows, and the range can use an index on col. Only equality is
// rewritten, and only a plain column; anything else is left to the index.
// A parameter v is cast to the column's type in the upper bound, where an
// interval beside it would make it an interval (see RewriteTruncEquality).
func truncRange(t Table, p predicate) string {
	if p.op != "=" {
		return ""
	}
	m := truncExpr.FindStringSubmatch(p.expr)
	if m == nil {
		return ""
	}
	col := m[2]
	if p.alias != "" {
		col = p.alias + "." + col
	}
	upper := upperBound(p.other, col, t.ColumnType)
	if upper == "" {
		return ""
	}
	return fmt.Sprintf("%s >= (%s) AND %s < %s + interval '1 %s'", col, p.other, col, upper, m[1])
}

func wildcard(t Table, p predicate) Finding {
	return Finding{Rule: RuleLeadingWildcard, Severity: SeverityWarn, Table: t.Key(),
		Title: "Pattern match cannot use a btree index on " + t.Key(),
		Detail: fmt.Sprintf("The query matches %s with a pattern that starts with a wildcard or ignores case. A btree index cannot serve that; "+
			"a trigram GIN index can.", p.expr),
		Proposal: "CREATE EXTENSION IF NOT EXISTS pg_trgm;\n" + createIndex(t, "gin", []string{p.expr}, "gin_trgm_ops")}
}

func jsonbIndex(t Table, p predicate) Finding {
	opclass := ""
	if p.op == "@>" {
		opclass = "jsonb_path_ops"
	}
	f := Finding{Rule: RuleJSONB, Severity: SeverityWarn, Table: t.Key(),
		Title:    "JSON search on " + t.Key() + " has no GIN index",
		Detail:   fmt.Sprintf("The query searches %s with %s, which only a GIN index can serve.", p.expr, p.op),
		Proposal: createIndex(t, "gin", []string{p.expr}, opclass)}
	if ix := leading(t, p.expr, "gin"); ix != nil {
		f.Rule, f.Severity, f.Proposal = RuleIndexNotChosen, SeverityInfo, ""
		f.Title = "GIN index exists but the planner read the whole table"
		f.Detail = fmt.Sprintf("%s has %s on %s, but the planner chose a full read.", t.Key(), ix.Name, p.expr)
	}
	return f
}

func wrongType(t Table, p predicate, hx *Index) Finding {
	return Finding{Rule: RuleWrongIndexType, Severity: SeverityWarn, Table: t.Key(),
		Title: "Hash index cannot serve a range on " + t.Key(),
		Detail: fmt.Sprintf("The query compares %s with %s, but its index %s is a hash index, which answers only equality. "+
			"A btree index answers both; once it exists the hash index can be dropped.", p.expr, p.op, hx.Name),
		Proposal: createIndex(t, "btree", []string{p.expr}, "")}
}

var nameWord = regexp.MustCompile(`[A-Za-z][A-Za-z0-9]*(?:_[A-Za-z0-9]+)*`)

var skipWords = map[string]bool{"both": true, "from": true, "leading": true, "trailing": true, "text": true}

// createIndex writes a CREATE INDEX CONCURRENTLY statement: it builds the
// index without blocking writes to the table.
func createIndex(t Table, method string, keys []string, opclass string) string {
	var words []string
	for _, w := range nameWord.FindAllString(strings.Join(keys, " "), -1) {
		if w = strings.ToLower(w); !skipWords[w] {
			words = append(words, w)
		}
	}
	name := strings.ToLower(t.Name) + "_" + strings.Join(words, "_")
	switch {
	case opclass == "gin_trgm_ops":
		name += "_trgm"
	case method == "gin":
		name += "_gin"
	}
	name = strings.TrimSuffix(name[:min(len(name), 59)], "_") + "_idx"
	using := ""
	if method != "btree" {
		using = " USING " + method
	}
	cols := make([]string, len(keys))
	for i, k := range keys {
		cols[i] = k
		if opclass != "" {
			cols[i] += " " + opclass
		}
	}
	return fmt.Sprintf("CREATE INDEX CONCURRENTLY IF NOT EXISTS %s ON %s%s (%s);", name, t.qualified(), using, strings.Join(cols, ", "))
}
