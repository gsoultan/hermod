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
	"strings"
)

// truncHead is date_trunc('<unit>', <column>) = as a person writes it, at the
// start of the text it is matched against. The column may be qualified.
var truncHead = regexp.MustCompile(`(?i)^date_trunc\s*\(\s*'(microseconds|milliseconds|second|minute|hour|day|week|month|quarter|year)'(?:::text)?\s*,\s*((?:"(?:[^"]|"")+"|[A-Za-z_][\w$]*)(?:\s*\.\s*(?:"(?:[^"]|"")+"|[A-Za-z_][\w$]*)){0,2})\s*\)\s*=`)

// rhsStops are the words that end the value compared, at the top level of a
// condition.
var rhsStops = map[string]bool{
	"AND": true, "OR": true, "ORDER": true, "GROUP": true, "LIMIT": true, "HAVING": true,
	"UNION": true, "INTERSECT": true, "EXCEPT": true, "RETURNING": true, "WINDOW": true,
	"OFFSET": true, "FETCH": true, "FOR": true, "THEN": true, "ELSE": true, "WHEN": true,
}

// RewriteTruncEquality rewrites each date_trunc('unit', col) = v in query as
// (col >= (v) AND col < (v) + interval '1 unit'). For a v that is the start of
// a unit, which is what comparing it to date_trunc implies, both select the
// same rows, and the range lets an index on col serve the condition, where
// the truncated column can only be served by an index on that expression. It
// works on the text as written, templates included, so the result is the
// query to put back in the editor. False means nothing was rewritten.
//
// A v holding a {{template}} or a $n parameter is sent as a parameter, and a
// parameter added to an interval is taken for an interval, so the upper
// bound casts it to the column's type: typeOf names it for the column as
// written, or returns "" to leave that condition as it is.
func RewriteTruncEquality(query string, typeOf func(col string) string) (string, bool) {
	var out strings.Builder
	changed := false
	for i := 0; i < len(query); {
		if j := skipQuoted(query, i); j > i {
			out.WriteString(query[i:j])
			i = j
			continue
		}
		if m := truncAt(query, i); m != nil {
			end := rhsEnd(query, m[1])
			unit, col := query[m[2]:m[3]], query[m[4]:m[5]]
			rhs := strings.TrimSpace(query[m[1]:end])
			if upper := upperBound(rhs, col, typeOf); upper != "" {
				fmt.Fprintf(&out, "(%s >= (%s) AND %s < %s + interval '1 %s')", col, rhs, col, upper, strings.ToLower(unit))
				// The space before the next clause stays as written.
				trail := query[m[1]:end]
				out.WriteString(trail[len(strings.TrimRight(trail, " \t\r\n")):])
				i, changed = end, true
				continue
			}
		}
		out.WriteByte(query[i])
		i++
	}
	return out.String(), changed
}

// upperBound is v as the upper bound's operand, cast when it is a parameter.
func upperBound(rhs, col string, typeOf func(string) string) string {
	if rhs == "" {
		return ""
	}
	if !bindsParameter(rhs) {
		return "(" + rhs + ")"
	}
	if typeOf == nil {
		return ""
	}
	if t := typeOf(col); t != "" {
		return "CAST(" + rhs + " AS " + t + ")"
	}
	return ""
}

// paramRef is a $n parameter.
var paramRef = regexp.MustCompile(`\$[0-9]+`)

func bindsParameter(s string) bool { return strings.Contains(s, "{{") || paramRef.MatchString(s) }

// truncAt matches truncHead at i, when i starts a word.
func truncAt(s string, i int) []int {
	if i > 0 && isIdentChar(s[i-1]) {
		return nil
	}
	m := truncHead.FindStringSubmatchIndex(s[i:])
	if m == nil {
		return nil
	}
	for k := range m {
		m[k] += i
	}
	return m
}

// rhsEnd is where the value that starts at i ends: at a closing parenthesis
// it did not open, a semicolon, or a word that starts the next clause.
func rhsEnd(s string, i int) int {
	var sc rhsScan
	for i < len(s) {
		if j := skipQuoted(s, i); j > i {
			i = j
			continue
		}
		next, stop := sc.step(s, i)
		if stop {
			return i
		}
		i = next
	}
	return i
}

// rhsScan tracks the parentheses and CASE blocks open in a value.
type rhsScan struct{ depth, cases int }

// step reads the character or word at i and returns where the next one
// starts, or stop when the value ends at i.
func (sc *rhsScan) step(s string, i int) (next int, stop bool) {
	c := s[i]
	switch {
	case c == '(':
		sc.depth++
	case c == ')':
		if sc.depth == 0 {
			return i, true
		}
		sc.depth--
	case c == ';':
		return i, sc.depth == 0
	case isIdentStart(c) && (i == 0 || !isIdentChar(s[i-1])):
		return sc.word(s, i)
	}
	return i + 1, false
}

func (sc *rhsScan) word(s string, i int) (next int, stop bool) {
	j := i
	for j < len(s) && isIdentChar(s[j]) {
		j++
	}
	switch w := strings.ToUpper(s[i:j]); {
	case w == "CASE":
		sc.cases++
	case w == "END" && sc.cases > 0:
		sc.cases--
	case sc.depth == 0 && sc.cases == 0 && rhsStops[w]:
		return i, true
	}
	return j, false
}

// skipQuoted returns the end of a string literal, quoted identifier, comment
// or {{template}} that starts at i, or i when none does.
func skipQuoted(s string, i int) int {
	switch {
	case s[i] == '\'' || s[i] == '"':
		q := s[i]
		for j := i + 1; j < len(s); j++ {
			if s[j] == q {
				if j+1 < len(s) && s[j+1] == q {
					j++
					continue
				}
				return j + 1
			}
		}
		return len(s)
	case strings.HasPrefix(s[i:], "--"):
		if n := strings.IndexByte(s[i:], '\n'); n >= 0 {
			return i + n
		}
		return len(s)
	case strings.HasPrefix(s[i:], "/*"):
		if n := strings.Index(s[i+2:], "*/"); n >= 0 {
			return i + 2 + n + 2
		}
		return len(s)
	case strings.HasPrefix(s[i:], "{{"):
		if n := strings.Index(s[i+2:], "}}"); n >= 0 {
			return i + 2 + n + 2
		}
		return len(s)
	}
	return i
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentChar(c byte) bool { return isIdentStart(c) || c == '$' || (c >= '0' && c <= '9') }

// lastPart is the column of a qualified name: created_at of l.created_at.
func lastPart(name string) string {
	parts := splitQualified(name)
	return parts[len(parts)-1]
}

// splitQualified splits a name on the dots outside double quotes.
func splitQualified(name string) []string {
	var parts []string
	start, quoted := 0, false
	for i := 0; i < len(name); i++ {
		switch {
		case name[i] == '"':
			quoted = !quoted
		case name[i] == '.' && !quoted:
			parts = append(parts, strings.TrimSpace(name[start:i]))
			start = i + 1
		}
	}
	return append(parts, strings.TrimSpace(name[start:]))
}

// unquoteName is an identifier as the catalog stores it: a quoted one as
// written, an unquoted one folded to lower case.
func unquoteName(name string) string {
	if len(name) >= 2 && name[0] == '"' && name[len(name)-1] == '"' {
		return strings.ReplaceAll(name[1:len(name)-1], `""`, `"`)
	}
	return strings.ToLower(name)
}
