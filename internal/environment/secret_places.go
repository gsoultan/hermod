// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package environment

import (
	"maps"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/security/secrets"
)

// SecretPlace is one place a secret is used: a step of the workflow, or a
// source or sink it reads from or writes to.
type SecretPlace struct {
	// Kind is "step", "source" or "sink".
	Kind string `json:"kind"`
	// Name is the step's label, or the connection's name.
	Name string `json:"name"`
	// Field is the setting that names the secret, as a person reads it
	// ("Headers › Authorization").
	Field string `json:"field,omitempty"`
	// NodeID and Value are set for a step: the node, and the setting as it
	// is saved, so the person sees how the secret is used. A connection's
	// settings are never sent: the other side's are for its administrators.
	NodeID string `json:"node_id,omitempty"`
	Value  string `json:"value,omitempty"`
}

// maxPlaceValue bounds the saved setting a place carries.
const maxPlaceValue = 300

// places maps a secret's name to where it is used.
type places map[string][]SecretPlace

func (ps places) add(name string, p SecretPlace) {
	if !slices.Contains(ps[name], p) {
		ps[name] = append(ps[name], p)
	}
}

func (ps places) merge(other places) {
	for _, name := range slices.Sorted(maps.Keys(other)) {
		for _, p := range other[name] {
			ps.add(name, p)
		}
	}
}

// only returns the places of the names given; nil when there are none.
func (ps places) only(names []string) map[string][]SecretPlace {
	var out map[string][]SecretPlace
	for _, name := range names {
		if len(ps[name]) == 0 {
			continue
		}
		if out == nil {
			out = map[string][]SecretPlace{}
		}
		out[name] = ps[name]
	}
	return out
}

// stepPlaces lists where the steps of wf use each secret.
func stepPlaces(wf storage.Workflow) places {
	out := places{}
	for _, n := range wf.Nodes {
		walkPaths(n.Config, nil, func(path []string, v string) {
			for _, ref := range secrets.References(v) {
				if ref.Kind == "secret" {
					out.add(ref.Name, SecretPlace{Kind: "step", Name: nodeLabel(n), NodeID: n.ID, Field: fieldLabel(path), Value: clip(v, maxPlaceValue)})
				}
			}
		})
	}
	return out
}

// connectionPlaces lists where a source's or sink's settings use each
// secret; kind is "source" or "sink".
func connectionPlaces(kind, name string, cfg map[string]string) places {
	out := places{}
	for _, key := range slices.Sorted(maps.Keys(cfg)) {
		for _, ref := range secrets.References(cfg[key]) {
			if ref.Kind == "secret" {
				out.add(ref.Name, SecretPlace{Kind: kind, Name: name, Field: fieldLabel([]string{key})})
			}
		}
	}
	return out
}

// placesFromUses reads the "sink warehouse" labels an instance that does not
// send places answers with.
func placesFromUses(uses map[string][]string) places {
	out := places{}
	for name, labels := range uses {
		for _, label := range labels {
			kind, conn, ok := strings.Cut(label, " ")
			if !ok || (kind != "source" && kind != "sink") {
				continue
			}
			out.add(name, SecretPlace{Kind: kind, Name: conn})
		}
	}
	return out
}

// walkPaths calls f with every string inside a decoded JSON value and the
// path to it. An entry of a list that names itself (a header's "key") is
// called by that name, any other by its position from 1.
func walkPaths(v any, path []string, f func(path []string, v string)) {
	switch x := v.(type) {
	case string:
		f(path, x)
	case map[string]any:
		for _, k := range slices.Sorted(maps.Keys(x)) {
			walkPaths(x[k], append(slices.Clip(path), k), f)
		}
	case []any:
		for i, e := range x {
			seg := strconv.Itoa(i + 1)
			if m, ok := e.(map[string]any); ok {
				for _, k := range []string{"key", "name", "header"} {
					if s, ok := m[k].(string); ok && strings.TrimSpace(s) != "" {
						seg = s
						break
					}
				}
			}
			walkPaths(e, append(slices.Clip(path), seg), f)
		}
	}
}

// fieldLabel reads a settings path as a person would: "Headers ›
// Authorization" for headers/Authorization. A segment that looks like a
// settings key (api_key, mcpServerUrl) is spelled out; one a person typed,
// such as a header's name, is kept as it is. A list entry's own value
// ("value", "headers/Authorization/value") is the entry itself.
func fieldLabel(path []string) string {
	if n := len(path); n > 1 && path[n-1] == "value" {
		path = path[:n-1]
	}
	parts := make([]string, 0, len(path))
	for _, seg := range path {
		parts = append(parts, humanizeKey(seg))
	}
	return strings.Join(parts, " › ")
}

var acronyms = map[string]string{
	"api": "API", "url": "URL", "uri": "URI", "id": "ID", "mcp": "MCP", "sql": "SQL", "http": "HTTP",
	"https": "HTTPS", "ssl": "SSL", "tls": "TLS", "jwt": "JWT", "aws": "AWS", "s3": "S3", "ai": "AI",
	"llm": "LLM", "smtp": "SMTP", "sftp": "SFTP", "ftp": "FTP", "ttl": "TTL", "db": "DB", "dsn": "DSN",
}

// humanizeKey spells out a settings key ("apiKey", "api_key" -> "API key").
// Anything else is returned as it is.
func humanizeKey(key string) string {
	if key == "" || !unicode.IsLower(rune(key[0])) || strings.ContainsFunc(key, func(r rune) bool {
		return r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		return key
	}
	var words []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			words = append(words, strings.ToLower(cur.String()))
			cur.Reset()
		}
	}
	for _, r := range key {
		switch {
		case r == '_':
			flush()
		case unicode.IsUpper(r):
			flush()
			cur.WriteRune(r)
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	for i, w := range words {
		if a, ok := acronyms[w]; ok {
			words[i] = a
		} else if i == 0 {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}

// clip shortens s to at most n bytes, on a rune boundary, marking the cut.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}
