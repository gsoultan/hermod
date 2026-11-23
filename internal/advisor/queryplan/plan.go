// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

// Package queryplan reads a lookup query's PostgreSQL plan against the
// indexes of the tables it touches, and says whether each table is reached
// the right way and which index would make it so.
//
// Reading is pure: ParsePlan takes EXPLAIN's JSON and Assess the catalog, so
// every rule is tested on recorded plans. Explain and LoadCatalog in pg.go
// are the only parts that touch a database.
package queryplan

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

// Scan is one place a plan reads a table or an index.
type Scan struct {
	// Node is the plan node type: "Seq Scan", "Index Scan", ...
	Node     string `json:"node"`
	Schema   string `json:"schema,omitempty"`
	Relation string `json:"relation,omitempty"`
	Alias    string `json:"alias,omitempty"`
	// Index is the index the scan reads, if it reads one.
	Index string `json:"index,omitempty"`
	// Conds are the conditions applied at this scan: its filter and index
	// condition, and the join conditions that compare its columns.
	Conds []string `json:"conds,omitempty"`
	// Rows is the planner's estimate of the rows the scan returns.
	Rows float64 `json:"rows"`
}

type planNode struct {
	NodeType    string     `json:"Node Type"`
	Schema      string     `json:"Schema"`
	Relation    string     `json:"Relation Name"`
	Alias       string     `json:"Alias"`
	IndexName   string     `json:"Index Name"`
	Filter      string     `json:"Filter"`
	IndexCond   string     `json:"Index Cond"`
	RecheckCond string     `json:"Recheck Cond"`
	HashCond    string     `json:"Hash Cond"`
	MergeCond   string     `json:"Merge Cond"`
	JoinFilter  string     `json:"Join Filter"`
	PlanRows    float64    `json:"Plan Rows"`
	Plans       []planNode `json:"Plans"`
}

// ParsePlan reads the output of EXPLAIN (VERBOSE, FORMAT JSON). VERBOSE
// matters: without it the plan names no schema and leaves columns
// unqualified, so a join condition cannot be tied to its table.
func ParsePlan(raw []byte) ([]Scan, error) {
	var doc []struct {
		Plan *planNode `json:"Plan"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	if len(doc) == 0 || doc[0].Plan == nil {
		return nil, errors.New("queryplan: the plan is empty")
	}
	var w planWalk
	w.walk(doc[0].Plan)
	w.attachJoins()
	return w.scans, nil
}

// planWalk gathers a plan's scans and its join conditions.
type planWalk struct {
	scans []Scan
	joins []string
}

func (w *planWalk) walk(n *planNode) {
	for _, c := range []string{n.HashCond, n.MergeCond, n.JoinFilter} {
		if c != "" {
			w.joins = append(w.joins, c)
		}
	}
	if strings.HasSuffix(n.NodeType, "Scan") && (n.Relation != "" || n.IndexName != "") {
		s := Scan{Node: n.NodeType, Schema: n.Schema, Relation: n.Relation, Alias: n.Alias, Index: n.IndexName, Rows: n.PlanRows}
		for _, c := range []string{n.IndexCond, n.RecheckCond, n.Filter} {
			if c != "" {
				s.Conds = append(s.Conds, c)
			}
		}
		w.scans = append(w.scans, s)
	}
	for i := range n.Plans {
		w.walk(&n.Plans[i])
	}
}

// attachJoins gives each scan the join conditions that compare its columns:
// a merge or hash join filters the table there, not in the scan.
func (w *planWalk) attachJoins() {
	for i := range w.scans {
		if w.scans[i].Alias == "" {
			continue
		}
		ref := aliasRef(w.scans[i].Alias)
		for _, j := range w.joins {
			if ref.MatchString(j) {
				w.scans[i].Conds = append(w.scans[i].Conds, j)
			}
		}
	}
}

var plainIdent = regexp.MustCompile(`^[a-z_][a-z0-9_$]*$`)

// quoteIdent quotes a name the way PostgreSQL prints it.
func quoteIdent(name string) string {
	if plainIdent.MatchString(name) {
		return name
	}
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// aliasRef matches a column qualified by alias, keeping the character before
// it in group 1 and the column in group 2.
func aliasRef(alias string) *regexp.Regexp {
	q := regexp.QuoteMeta(quoteIdent(alias))
	return regexp.MustCompile(`(^|[^\w".$])` + q + `\.("(?:[^"]|"")+"|[A-Za-z_][\w$]*)`)
}

// splitAnd splits a condition into its top-level AND terms, each without the
// parentheses that enclose it whole.
func splitAnd(cond string) []string {
	cond = stripOuter(cond)
	var out []string
	depth, start := 0, 0
	inQuote := false
	for i := 0; i < len(cond); i++ {
		switch c := cond[i]; {
		case c == '\'':
			inQuote = !inQuote
		case inQuote:
		case c == '(':
			depth++
		case c == ')':
			depth--
		case depth == 0 && strings.HasPrefix(cond[i:], " AND "):
			out = append(out, stripOuter(cond[start:i]))
			start = i + len(" AND ")
			i = start - 1
		}
	}
	return append(out, stripOuter(cond[start:]))
}

// stripOuter removes parentheses that enclose the whole text, repeatedly.
func stripOuter(s string) string {
	for {
		s = strings.TrimSpace(s)
		if len(s) < 2 || s[0] != '(' || s[len(s)-1] != ')' || closing(s, 0) != len(s)-1 {
			return s
		}
		s = s[1 : len(s)-1]
	}
}

// closing returns the index of the parenthesis that closes the one at open.
func closing(s string, open int) int {
	depth := 0
	inQuote := false
	for i := open; i < len(s); i++ {
		switch c := s[i]; {
		case c == '\'':
			inQuote = !inQuote
		case inQuote:
		case c == '(':
			depth++
		case c == ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// operators are the comparisons a predicate is split on, longest first so
// "~~*" is not read as "~~".
var operators = []string{"!~~*", "~~*", "!~~", "~~", "@>", "<@", "?|", "?&", "<>", ">=", "<=", "=", "<", ">", "?"}

// splitOp splits a predicate on its top-level comparison.
func splitOp(pred string) (lhs, op, rhs string, ok bool) {
	depth := 0
	inQuote := false
	for i := 0; i < len(pred); i++ {
		switch c := pred[i]; {
		case c == '\'':
			inQuote = !inQuote
		case inQuote:
		case c == '(':
			depth++
		case c == ')':
			depth--
		case depth == 0 && c == ' ':
			for _, o := range operators {
				if strings.HasPrefix(pred[i+1:], o+" ") {
					return strings.TrimSpace(pred[:i]), o, strings.TrimSpace(pred[i+len(o)+2:]), true
				}
			}
		}
	}
	return "", "", "", false
}
