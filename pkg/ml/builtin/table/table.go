// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

// Package table is the built-in ML engine's dataset in memory: typed columns,
// read from JSON rows, CSV or Excel, with the hermod-ml worker's typing rules
// (ml/worker/hermod_ml/datasets.py), so the same file gives the same columns
// on either engine.
//
// A column is number, string or bool. Missing values are not a type: a
// column of numbers with gaps is still number. A batch of values whose
// present values are all booleans is bool, all numbers number, and anything
// else string, with the other values rendered as text (ToText). Batches
// joined together keep a column's kind when every batch that holds a value
// agrees, and become string otherwise.
package table

import (
	"errors"
	"fmt"
	"math"
)

// The column kinds, as the worker's API names them.
const (
	Number = "number"
	String = "string"
	Bool   = "bool"
)

// Column is one column of a table. A number or bool column keeps its values
// in Num, NaN where missing, a bool as 1 or 0; a string column keeps them in
// Str, with Null true where missing.
type Column struct {
	Name string
	Kind string
	Num  []float64
	Str  []string
	Null []bool
}

// Table is a dataset: columns of Rows values each, in first-seen order.
type Table struct {
	Columns []*Column
	Rows    int
}

// Column returns the column named name, or nil.
func (t *Table) Column(name string) *Column {
	for _, c := range t.Columns {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// Missing reports whether row i of the column has no value.
func (c *Column) Missing(i int) bool {
	if c.Kind == String {
		return c.Null[i]
	}
	return math.IsNaN(c.Num[i])
}

// Value is row i as JSON gives it back: a float64, string or bool, or nil.
func (c *Column) Value(i int) any {
	if c.Missing(i) {
		return nil
	}
	switch c.Kind {
	case String:
		return c.Str[i]
	case Bool:
		return c.Num[i] == 1
	}
	return c.Num[i]
}

// Text is row i as text: a string as it is, anything else as ToText renders
// it, and "" when missing.
func (c *Column) Text(i int) string {
	if c.Missing(i) {
		return ""
	}
	if c.Kind == String {
		return c.Str[i]
	}
	return ToText(c.Value(i))
}

// ErrNoColumns is returned for rows that are all empty objects.
var ErrNoColumns = errors.New("every row is empty; rows need at least one column")

// FromRecords types rows of JSON values (as encoding/json decodes them) into
// a table. Columns appear in the order rows first name them; a map does not
// keep its keys' order, so the keys new in one row are sorted (FromJSON keeps
// the order the JSON gave). A key a row lacks is missing there. Objects and
// arrays are kept as their JSON text.
func FromRecords(rows []map[string]any) (*Table, error) {
	return fromOrdered(rows, stableOrder(rows))
}

// fromOrdered types rows into columns named order.
func fromOrdered(rows []map[string]any, order []string) (*Table, error) {
	if len(rows) > 0 && len(order) == 0 {
		return nil, ErrNoColumns
	}
	t := &Table{Rows: len(rows)}
	values := make([]any, len(rows))
	for _, name := range order {
		for i, r := range rows {
			values[i] = r[name]
		}
		t.Columns = append(t.Columns, columnOf(name, values))
	}
	return t, nil
}

// stableOrder is the columns of rows in the order a row first names them,
// the keys new in one row sorted by name.
func stableOrder(rows []map[string]any) []string {
	var order []string
	seen := map[string]bool{}
	for _, r := range rows {
		var fresh []string
		for k := range r {
			if !seen[k] {
				fresh = append(fresh, k)
			}
		}
		sortStrings(fresh)
		for _, k := range fresh {
			seen[k] = true
			order = append(order, k)
		}
	}
	return order
}

// columnOf types one column of values.
func columnOf(name string, values []any) *Column {
	kind := kindOf(values)
	c := newColumn(name, kind, len(values))
	for i, v := range values {
		c.set(i, v)
	}
	return c
}

// kindOf is the kind the present values make: bool when all are booleans,
// number when all are numbers, string otherwise. With none present it is
// "", which does not vote when tables are joined.
func kindOf(values []any) string {
	kind := ""
	for _, v := range values {
		if isNull(v) {
			continue
		}
		var k string
		switch v.(type) {
		case bool:
			k = Bool
		case float64, float32, int, int64, int32:
			k = Number
		default:
			return String
		}
		if kind != "" && kind != k {
			return String
		}
		kind = k
	}
	return kind
}

func isNull(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case float64:
		return math.IsNaN(x)
	case float32:
		return math.IsNaN(float64(x))
	}
	return false
}

// newColumn makes an empty column of n missing values. A kind of "" (no
// value at all) is held as number until a join gives it another.
func newColumn(name, kind string, n int) *Column {
	c := &Column{Name: name, Kind: kind}
	if kind == "" {
		c.Kind = Number
	}
	if c.Kind == String {
		c.Str, c.Null = make([]string, n), make([]bool, n)
		for i := range c.Null {
			c.Null[i] = true
		}
		return c
	}
	c.Num = make([]float64, n)
	for i := range c.Num {
		c.Num[i] = math.NaN()
	}
	return c
}

// set stores v at row i, converted to the column's kind.
func (c *Column) set(i int, v any) {
	if isNull(v) {
		return
	}
	switch c.Kind {
	case String:
		if s, ok := v.(string); ok {
			c.Str[i] = s
		} else {
			c.Str[i] = ToText(v)
		}
		c.Null[i] = false
	case Bool:
		if b, _ := v.(bool); b {
			c.Num[i] = 1
		} else {
			c.Num[i] = 0
		}
	default:
		c.Num[i] = toFloat(v)
	}
}

func toFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case int32:
		return float64(x)
	}
	return math.NaN()
}

// Records renders rows [from, to) as JSON-ready objects, a missing value as
// nil.
func (t *Table) Records(from, to int) []map[string]any {
	to = min(to, t.Rows)
	if from >= to {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, to-from)
	for i := from; i < to; i++ {
		r := make(map[string]any, len(t.Columns))
		for _, c := range t.Columns {
			r[c.Name] = c.Value(i)
		}
		out = append(out, r)
	}
	return out
}

// Slice returns rows [from, to) as a table of their own.
func (t *Table) Slice(from, to int) *Table {
	to = min(to, t.Rows)
	from = min(from, to)
	out := &Table{Rows: to - from}
	for _, c := range t.Columns {
		s := &Column{Name: c.Name, Kind: c.Kind}
		if c.Kind == String {
			s.Str, s.Null = c.Str[from:to], c.Null[from:to]
		} else {
			s.Num = c.Num[from:to]
		}
		out.Columns = append(out.Columns, s)
	}
	return out
}

// Take returns the rows idx, in that order, as a table of their own.
func (t *Table) Take(idx []int) *Table {
	out := &Table{Rows: len(idx)}
	for _, c := range t.Columns {
		s := &Column{Name: c.Name, Kind: c.Kind}
		if c.Kind == String {
			s.Str, s.Null = make([]string, len(idx)), make([]bool, len(idx))
			for i, r := range idx {
				s.Str[i], s.Null[i] = c.Str[r], c.Null[r]
			}
		} else {
			s.Num = make([]float64, len(idx))
			for i, r := range idx {
				s.Num[i] = c.Num[r]
			}
		}
		out.Columns = append(out.Columns, s)
	}
	return out
}

// checkUnique refuses a header that names a column twice.
func checkUnique(header []string) error {
	seen := map[string]bool{}
	for _, h := range header {
		if seen[h] {
			return fmt.Errorf("column %q appears more than once", h)
		}
		seen[h] = true
	}
	return nil
}
