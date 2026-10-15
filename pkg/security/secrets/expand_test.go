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
	"context"
	"errors"
	"strings"
	"testing"
)

// scopedStub answers GetScoped from a map keyed "vhost/name"; a vhost of ""
// is the global manager.
type scopedStub struct {
	values map[string]string
	err    error
}

func (s scopedStub) GetScoped(_ context.Context, vhost, key string) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	if v, ok := s.values[vhost+"/"+key]; ok {
		return v, nil
	}
	return s.values["/"+key], nil
}

func TestExpandReferencesResolvesEverySpellingInsideAValue(t *testing.T) {
	t.Setenv("HERMOD_ENV_PG_HOST", "db.internal")
	t.Setenv("HERMOD_ENV_PG_PORT", "6432")
	mgr := scopedStub{values: map[string]string{
		"sales/DB_PASS": "s3cr3t",
		"/API_KEY":      "global-key",
		"/REGION":       "from-manager",
	}}

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"standard secret", "{{secret.DB_PASS}}", "s3cr3t"},
		{"standard env", "{{ env.PG_HOST }}", "db.internal"},
		{"standard spelling inside a value", "postgres://app:{{secret.DB_PASS}}@{{env.PG_HOST}}:{{env.PG_PORT}}/sales", "postgres://app:s3cr3t@db.internal:6432/sales"},
		{"standard spelling, any case", "{{Secret.DB_PASS}}", "s3cr3t"},
		// A leading dot is a message field, as {{.after.id}} is: never a secret.
		{"a dotted field named secret is a field", "{{.secret.DB_PASS}}", "{{.secret.DB_PASS}}"},
		{"a deeper path is not a reference", "{{secret.DB_PASS.x}}", "{{secret.DB_PASS.x}}"},
		{"bare standard text is text", "secret.DB_PASS", "secret.DB_PASS"},
		{"legacy whole value", "secret:DB_PASS", "s3cr3t"},
		{"legacy whole value in braces", "{{secret:DB_PASS}}", "s3cr3t"},
		{"whole env value in braces", "{{env:PG_HOST}}", "db.internal"},
		// Only the legacy secret: form is read bare; "env:prod" may be a tag.
		{"bare env text is text", "env:PG_HOST", "env:PG_HOST"},
		{"embedded colon form", "postgres://app:{{secret:DB_PASS}}@{{env:PG_HOST}}:{{env:PG_PORT}}/sales", "postgres://app:s3cr3t@db.internal:6432/sales"},
		{"embedded function form", `Bearer {{ secret("API_KEY") }}`, "Bearer global-key"},
		{"single quotes and case", `{{SECRET('DB_PASS')}}-{{Env('PG_HOST')}}`, "s3cr3t-db.internal"},
		{"env prefers the secret manager, as env() does", `{{env("REGION")}}`, "from-manager"},
		{"fallback when missing", `{{env("NOPE", "eu-west-1")}}`, "eu-west-1"},
		{"a field token is left for the message", "{{.after.id}}/{{secret:DB_PASS}}", "{{.after.id}}/s3cr3t"},
		{"another function is left for the message", `{{upper(source.name)}}`, `{{upper(source.name)}}`},
		{"an unclosed token is literal", "a{{secret:DB_PASS", "a{{secret:DB_PASS"},
		{"plain text untouched", "localhost", "localhost"},
		{"a resolved value is not scanned again", "{{secret:LOOP}}", "{{secret:DB_PASS}}"},
	}
	mgr.values["/LOOP"] = "{{secret:DB_PASS}}"
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ExpandReferences(t.Context(), mgr, "sales", tc.in)
			if err != nil {
				t.Fatalf("ExpandReferences(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("ExpandReferences(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// A JSON config value -- an HTTP sink's headers -- stays JSON when a secret
// holding a quote or a backslash is put into one of its strings.
func TestExpandReferencesKeepsAJSONValueValid(t *testing.T) {
	mgr := scopedStub{values: map[string]string{"/TOKEN": `a"b\c`}}
	got, err := ExpandReferences(t.Context(), mgr, "", `{"Authorization": "Bearer {{secret:TOKEN}}", "X-Id": "{{.after.id}}"}`)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"Authorization":"Bearer a\"b\\c","X-Id":"{{.after.id}}"}`
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

// A reference that resolves to nothing fails loudly, naming it and never a
// value -- connecting with the literal token as a password helps nobody.
func TestExpandReferencesReportsWhatItCouldNotResolve(t *testing.T) {
	mgr := scopedStub{values: map[string]string{"/A": "VALUE-123"}}
	_, err := ExpandReferences(t.Context(), mgr, "", "{{secret.A}}:{{secret.MISSING}}@{{env.ALSO_MISSING}}")
	if err == nil {
		t.Fatal("want an error for unresolved references")
	}
	for _, name := range []string{`secret "MISSING"`, `env "ALSO_MISSING"`} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error %q does not name %s", err, name)
		}
	}
	if strings.Contains(err.Error(), "VALUE-123") {
		t.Errorf("error leaks a value: %q", err)
	}
}

// A bare `secret:KEY` that resolves to nothing passes through unchanged, as
// it always has: a config may hold that text for another reason.
func TestExpandReferencesLeavesAnUnresolvedLegacyValueAsItWas(t *testing.T) {
	got, err := ExpandReferences(t.Context(), scopedStub{}, "", "secret:NOT_THERE")
	if err != nil {
		t.Fatal(err)
	}
	if got != "secret:NOT_THERE" {
		t.Errorf("got %q", got)
	}
}

func TestExpandReferencesSurfacesAStoreFailure(t *testing.T) {
	_, err := ExpandReferences(t.Context(), scopedStub{err: errors.New("store down")}, "v", "{{secret:A}}")
	if err == nil || !strings.Contains(err.Error(), "store down") {
		t.Fatalf("err = %v, want the store failure", err)
	}
}

// Only variables the operator put under HERMOD_ENV_ are readable: a workflow
// editor must not reach HERMOD_JWT_SECRET or anything else the process holds.
func TestLookupEnvVarReadsOnlyTheEnvPrefix(t *testing.T) {
	t.Setenv("HERMOD_ENV_REGION", "eu")
	t.Setenv("HERMOD_JWT_SECRET", "signing-key")
	t.Setenv("PLAIN_VAR", "plain")

	tests := []struct{ key, want string }{
		{"REGION", "eu"},
		{"HERMOD_ENV_REGION", "eu"},
		{"HERMOD_JWT_SECRET", ""},
		{"PLAIN_VAR", ""},
		{"../REGION", ""},
		{"", ""},
	}
	for _, tc := range tests {
		if got := LookupEnvVar(tc.key); got != tc.want {
			t.Errorf("LookupEnvVar(%q) = %q, want %q", tc.key, got, tc.want)
		}
	}
}

// A node's settings resolve their references at any depth, skip what they are
// told to, and leave the tree they were given untouched.
func TestExpandTreeResolvesNestedSettingsWithoutModifyingThem(t *testing.T) {
	t.Setenv("HERMOD_ENV_MODEL", "gpt-x")
	mgr := scopedStub{values: map[string]string{"v/KEY": "k1"}}
	cfg := map[string]any{
		"key":           "{{secret:KEY}}",
		"model":         `{{env("MODEL")}}`,
		"plain":         "secret:KEY",
		"queryTemplate": "SELECT {{secret:KEY}}",
		"server":        map[string]any{"headers": map[string]any{"Authorization": `Bearer {{secret("KEY")}}`}},
		"steps":         []any{"{{secret:KEY}}", 3},
		"cases":         []map[string]any{{"value": "{{env:MODEL}}"}},
		"count":         5,
	}
	out, changed, err := ExpandTree(t.Context(), mgr, "v", cfg, func(k string) bool { return k == "queryTemplate" })
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	got := out.(map[string]any)
	checks := map[string]any{
		"key":           "k1",
		"model":         "gpt-x",
		"plain":         "secret:KEY",
		"queryTemplate": "SELECT {{secret:KEY}}",
		"count":         5,
	}
	for k, want := range checks {
		if got[k] != want {
			t.Errorf("%s = %#v, want %#v", k, got[k], want)
		}
	}
	if h := got["server"].(map[string]any)["headers"].(map[string]any)["Authorization"]; h != "Bearer k1" {
		t.Errorf("nested header = %v", h)
	}
	if s := got["steps"].([]any)[0]; s != "k1" {
		t.Errorf("list item = %v", s)
	}
	if c := got["cases"].([]map[string]any)[0]["value"]; c != "gpt-x" {
		t.Errorf("case value = %v", c)
	}
	if cfg["key"] != "{{secret:KEY}}" || cfg["server"].(map[string]any)["headers"].(map[string]any)["Authorization"] != `Bearer {{secret("KEY")}}` || cfg["steps"].([]any)[0] != "{{secret:KEY}}" {
		t.Error("the settings passed in were modified")
	}

	same := map[string]any{"a": "x", "b": map[string]any{"c": "{{.after.id}}"}}
	_, changed, err = ExpandTree(t.Context(), mgr, "v", same, nil)
	if err != nil || changed {
		t.Fatalf("a tree with no references: changed=%v err=%v", changed, err)
	}
}

func TestExpandTreeNamesTheSettingAnUnresolvedReferenceIsIn(t *testing.T) {
	_, _, err := ExpandTree(t.Context(), scopedStub{}, "", map[string]any{"server": map[string]any{"url": "{{secret:NOPE}}"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "server: url:") || !strings.Contains(err.Error(), `secret "NOPE"`) {
		t.Fatalf("err = %v", err)
	}
}
