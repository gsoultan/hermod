// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package secrets

import (
	"regexp"
	"strings"
)

// Ref is one secret or env variable a value reads: Kind is "secret" or "env".
type Ref struct {
	Kind string
	Name string
}

// exprReference is secret.NAME, env.NAME, secret("NAME") or env('NAME', ...)
// as an expression reads it. Where it may stand is checked by hand: RE2 has
// no look-around.
var exprReference = regexp.MustCompile(`(?i)\b(secret|env)(?:\.([A-Za-z_][A-Za-z0-9_]*)|\s*\(\s*["']([A-Za-z_][A-Za-z0-9_]*)["'])`)

// callInText says text outside {{ }} holds a function call, as an expression
// such as concat(secret.KEY, "x") does and prose rarely does.
var callInText = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*\(`)

// References lists, in the order they first appear and each once, the
// secrets and env variables value reads in any spelling the engine resolves:
// a {{ }} token (secret.NAME, secret("NAME"), secret:NAME), a secret inside a
// function in a token ({{upper(secret.NAME)}}), an expression written bare
// (secret("NAME"), concat(secret.NAME, "x")), and the legacy bare secret:NAME.
// A message field ({{.secret.x}}, source.secret.x) and a deeper path
// (secret.A.b) are not references.
func References(value string) []Ref {
	var out []Ref
	add := func(kind, name string) {
		r := Ref{Kind: strings.ToLower(kind), Name: name}
		for _, have := range out {
			if have == r {
				return
			}
		}
		out = append(out, r)
	}
	trimmed := strings.TrimSpace(value)
	if hasPrefixFold(trimmed, "secret:") {
		if ref, ok := parseReference(trimmed); ok {
			add(ref.kind, ref.name)
		}
		return out
	}
	var text strings.Builder
	rest := value
	for {
		start := strings.Index(rest, "{{")
		if start < 0 {
			break
		}
		end := strings.Index(rest[start+2:], "}}")
		if end < 0 {
			break
		}
		end += start + 2
		text.WriteString(rest[:start])
		text.WriteByte(' ')
		inner := rest[start+2 : end]
		if ref, ok := parseReference(inner); ok {
			add(ref.kind, ref.name)
		} else {
			scanExpression(inner, add)
		}
		rest = rest[end+2:]
	}
	text.WriteString(rest)
	if bare := text.String(); callInText.MatchString(bare) {
		scanExpression(bare, add)
	}
	return out
}

// scanExpression adds every reference expr reads.
func scanExpression(expr string, add func(kind, name string)) {
	for _, m := range exprReference.FindAllStringSubmatchIndex(expr, -1) {
		if m[0] > 0 {
			if c := expr[m[0]-1]; c == '.' || c == '$' || isWordByte(c) {
				continue
			}
		}
		kind := expr[m[2]:m[3]]
		if m[4] >= 0 { // dotted: a deeper path or a method call is not one
			if m[1] < len(expr) && (expr[m[1]] == '.' || expr[m[1]] == '(') {
				continue
			}
			add(kind, expr[m[4]:m[5]])
			continue
		}
		add(kind, expr[m[6]:m[7]])
	}
}

func isWordByte(c byte) bool {
	return c == '_' || ('0' <= c && c <= '9') || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')
}
