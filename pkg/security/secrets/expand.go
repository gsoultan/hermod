// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

package secrets

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

// EnvVarPrefix is the namespace env references read from the process
// environment. Only variables the operator deliberately put under it are
// readable, so a workflow editor reaches HERMOD_ENV_REGION but never
// HERMOD_JWT_SECRET, a database password or anything else the process holds.
const EnvVarPrefix = "HERMOD_ENV_"

// referenceName is what a secret or env reference may name: the rule
// storage.ValidVHostSecretName applies, with no length cap of its own.
var referenceName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// LookupEnvVar reads EnvVarPrefix+key from the process environment -- or key
// itself when it already carries the prefix -- and nothing else.
func LookupEnvVar(key string) string {
	if !referenceName.MatchString(key) {
		return ""
	}
	if !strings.HasPrefix(key, EnvVarPrefix) {
		key = EnvVarPrefix + key
	}
	return os.Getenv(key)
}

// ReadEnv answers an env reference for vhost: the secret manager first, which
// is what env() has always read, then the HERMOD_ENV_ variable.
func ReadEnv(ctx context.Context, mgr ScopedManager, vhost, key string) (string, error) {
	if mgr != nil {
		v, err := mgr.GetScoped(ctx, vhost, key)
		if err != nil {
			return "", err
		}
		if v != "" {
			return v, nil
		}
	}
	return LookupEnvVar(key), nil
}

// reference is one secret or env reference read out of a config value.
type reference struct {
	kind        string // "secret" or "env"
	name        string
	fallback    string
	hasFallback bool
}

func (r reference) String() string { return fmt.Sprintf("%s %q", r.kind, r.name) }

// DottedReference reads the inside of a {{ }} token as the standard spelling,
// secret.NAME or env.NAME, and returns its kind ("secret" or "env") and name.
// The kind is case-insensitive. A leading dot makes it a message field, as in
// {{.after.id}}, and a deeper path (secret.A.b) is not a reference either.
func DottedReference(text string) (kind, name string, ok bool) {
	text = strings.TrimSpace(text)
	for _, k := range []string{"secret", "env"} {
		if rest, found := cutPrefixFold(text, k+"."); found && referenceName.MatchString(rest) {
			return k, rest, true
		}
	}
	return "", "", false
}

// parseReference reads the inside of a {{ }} token, or a whole bare value, as
// a reference: the standard secret.NAME and env.NAME, and the older
// secret:NAME, env:NAME, secret("NAME"), env('NAME', "fallback"). Function
// names are case-insensitive, as the evaluator's are. Anything else -- a
// field, another function -- is not a reference.
func parseReference(text string) (reference, bool) {
	text = strings.TrimSpace(text)
	if kind, name, ok := DottedReference(text); ok {
		return reference{kind: kind, name: name}, true
	}
	for _, kind := range []string{"secret", "env"} {
		if rest, ok := cutPrefixFold(text, kind+":"); ok {
			name := strings.TrimSpace(rest)
			if referenceName.MatchString(name) {
				return reference{kind: kind, name: name}, true
			}
			return reference{}, false
		}
		rest, ok := cutPrefixFold(text, kind)
		if !ok {
			continue
		}
		rest = strings.TrimSpace(rest)
		if !strings.HasPrefix(rest, "(") || !strings.HasSuffix(rest, ")") {
			continue
		}
		args, ok := literalArgs(rest[1 : len(rest)-1])
		if !ok || len(args) == 0 || len(args) > 2 || !referenceName.MatchString(args[0]) {
			return reference{}, false
		}
		ref := reference{kind: kind, name: args[0]}
		if len(args) == 2 {
			ref.fallback, ref.hasFallback = args[1], true
		}
		return ref, true
	}
	return reference{}, false
}

func hasPrefixFold(s, prefix string) bool {
	_, ok := cutPrefixFold(s, prefix)
	return ok
}

func cutPrefixFold(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
		return s[len(prefix):], true
	}
	return s, false
}

// literalArgs splits a call's arguments when every one is a quoted string;
// a field or a nested call makes it something only a message can answer.
func literalArgs(text string) ([]string, bool) {
	var args []string
	text = strings.TrimSpace(text)
	for text != "" {
		q := text[0]
		if q != '"' && q != '\'' {
			return nil, false
		}
		end := strings.IndexByte(text[1:], q)
		if end < 0 {
			return nil, false
		}
		args = append(args, text[1:end+1])
		text = strings.TrimSpace(text[end+2:])
		if text == "" {
			break
		}
		if text[0] != ',' {
			return nil, false
		}
		text = strings.TrimSpace(text[1:])
		if text == "" {
			return nil, false
		}
	}
	return args, true
}

// ExpandReferences replaces every secret and env reference in a config value,
// for the vhost the config belongs to, and leaves every other {{ }} token as
// it was for whatever resolves the value per message.
//
// It reads, anywhere in the value, the standard spelling
//
//	{{secret.NAME}}  {{env.NAME}}
//
// and the older ones, which keep working:
//
//	{{secret:NAME}}  {{secret("NAME")}}  {{secret("NAME", "fallback")}}
//	{{env:NAME}}     {{env("NAME")}}     {{env("NAME", "fallback")}}
//
// and, as the whole value, secret:NAME. A secret is read from the
// vhost's secrets and then the global manager; an env reference from the same
// lookup and then the HERMOD_ENV_ variable (see ReadEnv).
//
// It is one forward pass: a resolved value is never scanned again, so a
// secret holding "{{secret:OTHER}}" cannot read OTHER. A value that is a JSON
// object or array has references replaced inside its strings, escaped, so a
// secret holding a quote leaves it JSON.
//
// A reference that resolves to nothing, with no fallback, is an error naming
// it -- never its value. A whole value of the legacy secret:NAME form is the
// exception: it has always passed through unchanged, and still does.
func ExpandReferences(ctx context.Context, mgr ScopedManager, vhost, value string) (string, error) {
	return expandValue(ctx, mgr, vhost, value, true)
}

// ExpandTokenReferences is ExpandReferences for {{ }} tokens only: a bare
// secret:NAME is left as text. It is for a workflow node's settings, where
// that spelling never meant a secret and a Set value may well hold it.
func ExpandTokenReferences(ctx context.Context, mgr ScopedManager, vhost, value string) (string, error) {
	if !strings.Contains(value, "{{") {
		return value, nil
	}
	return expandValue(ctx, mgr, vhost, value, false)
}

// ExpandTree is ExpandTokenReferences over a node's settings: every string in
// v, inside maps and lists to any depth. A value under a key skip reports true
// is left as it is, with everything beneath it. The tree passed in is never
// modified; what changed is copied, and changed reports whether anything was.
func ExpandTree(ctx context.Context, mgr ScopedManager, vhost string, v any, skip func(key string) bool) (out any, changed bool, err error) {
	t := treeExpander{ctx: ctx, mgr: mgr, vhost: vhost, skip: skip}
	return t.expand(v)
}

// ExpandSettings is ExpandTree for a node's settings map.
func ExpandSettings(ctx context.Context, mgr ScopedManager, vhost string, settings map[string]any, skip func(key string) bool) (map[string]any, bool, error) {
	t := treeExpander{ctx: ctx, mgr: mgr, vhost: vhost, skip: skip}
	return t.expandMap(settings)
}

type treeExpander struct {
	ctx   context.Context
	mgr   ScopedManager
	vhost string
	skip  func(key string) bool
}

func (t treeExpander) expand(v any) (any, bool, error) {
	switch v := v.(type) {
	case string:
		s, err := ExpandTokenReferences(t.ctx, t.mgr, t.vhost, v)
		if err != nil {
			return nil, false, err
		}
		return s, s != v, nil
	case map[string]any:
		m, changed, err := t.expandMap(v)
		return m, changed, err
	case []map[string]any:
		return expandList(v, t.expandMap)
	case []any:
		return expandList(v, t.expand)
	}
	return v, false, nil
}

// expandMap copies m only once a value under it changes.
func (t treeExpander) expandMap(m map[string]any) (map[string]any, bool, error) {
	var copied map[string]any
	for k, val := range m {
		if t.skip != nil && t.skip(k) {
			continue
		}
		nv, changed, err := t.expand(val)
		if err != nil {
			return nil, false, fmt.Errorf("%s: %w", k, err)
		}
		if !changed {
			continue
		}
		if copied == nil {
			copied = make(map[string]any, len(m))
			for ck, cv := range m {
				copied[ck] = cv
			}
		}
		copied[k] = nv
	}
	if copied == nil {
		return m, false, nil
	}
	return copied, true, nil
}

// expandList copies list only once an item changes.
func expandList[T any](list []T, expand func(T) (T, bool, error)) ([]T, bool, error) {
	var copied []T
	for i, item := range list {
		nv, changed, err := expand(item)
		if err != nil {
			return nil, false, fmt.Errorf("[%d]: %w", i, err)
		}
		if !changed {
			continue
		}
		if copied == nil {
			copied = append([]T(nil), list...)
		}
		copied[i] = nv
	}
	if copied == nil {
		return list, false, nil
	}
	return copied, true, nil
}

func expandValue(ctx context.Context, mgr ScopedManager, vhost, value string, bare bool) (string, error) {
	if !mayHoldReference(value) {
		return value, nil
	}
	x := expander{ctx: ctx, mgr: mgr, vhost: vhost}

	trimmed := strings.TrimSpace(value)
	if !strings.Contains(trimmed, "{{") {
		if !bare {
			return value, nil
		}
		// Bare, only the legacy secret:NAME form is a reference: "env:prod"
		// may well be a tag or a topic.
		ref, ok := parseReference(trimmed)
		if !ok || ref.kind != "secret" || !hasPrefixFold(trimmed, "secret:") {
			return value, nil
		}
		v, found, err := x.resolve(ref)
		if err != nil {
			return "", err
		}
		if !found {
			return value, nil
		}
		return v, nil
	}

	var out string
	if expanded, ok := x.expandJSON(trimmed); ok {
		out = expanded
	} else {
		out = x.expandText(value)
	}
	if x.err != nil {
		return "", x.err
	}
	if len(x.missing) > 0 {
		names := make([]string, len(x.missing))
		for i, ref := range x.missing {
			names[i] = ref.String()
		}
		return "", fmt.Errorf("unresolved reference(s): %s", strings.Join(names, ", "))
	}
	return out, nil
}

// mayHoldReference is the cheap test that keeps the common value -- a host,
// a port, a table name -- from being scanned at all.
func mayHoldReference(value string) bool {
	lower := strings.ToLower(value)
	return strings.Contains(lower, "secret") || strings.Contains(lower, "env")
}

type expander struct {
	ctx     context.Context
	mgr     ScopedManager
	vhost   string
	err     error
	missing []reference
}

func (x *expander) resolve(ref reference) (string, bool, error) {
	var (
		v   string
		err error
	)
	if ref.kind == "env" {
		v, err = ReadEnv(x.ctx, x.mgr, x.vhost, ref.name)
	} else if x.mgr != nil {
		v, err = x.mgr.GetScoped(x.ctx, x.vhost, ref.name)
	}
	if err != nil {
		return "", false, fmt.Errorf("reading %s: %w", ref, err)
	}
	if v == "" && ref.hasFallback {
		return ref.fallback, true, nil
	}
	return v, v != "", nil
}

// expandText replaces the references among the {{ }} tokens of s.
func (x *expander) expandText(s string) string {
	var out strings.Builder
	for {
		start := strings.Index(s, "{{")
		if start < 0 {
			break
		}
		end := strings.Index(s[start+2:], "}}")
		if end < 0 {
			break
		}
		end += start + 2
		out.WriteString(s[:start])
		token := s[start : end+2]
		if ref, ok := parseReference(s[start+2 : end]); ok {
			v, found, err := x.resolve(ref)
			switch {
			case err != nil:
				if x.err == nil {
					x.err = err
				}
				out.WriteString(token)
			case !found:
				x.missing = append(x.missing, ref)
				out.WriteString(token)
			default:
				out.WriteString(v)
			}
		} else {
			out.WriteString(token)
		}
		s = s[end+2:]
	}
	out.WriteString(s)
	return out.String()
}

// expandJSON expands the strings of a JSON object or array -- keys and
// values -- and writes it back; one with nothing to replace comes back as it
// was. It reports false for anything that is not such a document.
func (x *expander) expandJSON(s string) (string, bool) {
	if s == "" || (s[0] != '{' && s[0] != '[') {
		return "", false
	}
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return "", false
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return "", false
	}
	changed := false
	doc = x.walk(doc, &changed)
	if !changed {
		return s, true
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(doc); err != nil {
		return "", false
	}
	return strings.TrimSuffix(buf.String(), "\n"), true
}

func (x *expander) walk(v any, changed *bool) any {
	switch t := v.(type) {
	case string:
		out := x.expandText(t)
		if out != t {
			*changed = true
		}
		return out
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, val := range t {
			nk := x.expandText(k)
			if nk != k {
				*changed = true
			}
			m[nk] = x.walk(val, changed)
		}
		return m
	case []any:
		for i := range t {
			t[i] = x.walk(t[i], changed)
		}
		return t
	}
	return v
}
