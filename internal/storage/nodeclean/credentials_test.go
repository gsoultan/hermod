// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package nodeclean_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/internal/storage/nodeclean"
)

func step(kind string, cfg map[string]any) storage.WorkflowNode {
	cfg["transType"] = kind
	return storage.WorkflowNode{ID: "node_1", Type: "transformation", Config: cfg}
}

func typedFields(nodes []storage.WorkflowNode) []string {
	var out []string
	for _, c := range nodeclean.TypedCredentials(nodes) {
		f := c.Field
		if c.Header != "" {
			f += "." + c.Header
		}
		out = append(out, f+"="+c.Value)
	}
	return out
}

func TestTypedCredentialsAreFoundOnTheStepsThatReadThem(t *testing.T) {
	nodes := []storage.WorkflowNode{
		step("api_lookup", map[string]any{"authType": "basic", "username": "apiuser", "password": "pw1",
			"token": "", "headers": `{"Authorization":"Bearer t0k","Accept":"application/json"}`}),
		step("ai_prompt", map[string]any{"apiKey": "sk-1", "fallbackApiKey": "{{secret.FB}}"}),
		step("redis_lookup", map[string]any{"password": "pw2", "key": "user:{{.id}}"}),
		{ID: "n9", Type: "ai_classify", Config: map[string]any{"apiKey": "sk-2"}},
		step("mapping", map[string]any{"password": "not read by this step"}),
	}
	got := strings.Join(typedFields(nodes), " ")
	for _, want := range []string{"username=apiuser", "password=pw1", "headers.Authorization=Bearer t0k",
		"apiKey=sk-1", "password=pw2", "apiKey=sk-2"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in %s", want, got)
		}
	}
	for _, not := range []string{"Accept", "FB", "user:", "not read"} {
		if strings.Contains(got, not) {
			t.Errorf("%q is not a typed credential: %s", not, got)
		}
	}
}

func TestRewriteReplacesEachCredentialAndLeavesTheInputAlone(t *testing.T) {
	nodes := []storage.WorkflowNode{
		step("api_lookup", map[string]any{"password": "pw1", "headers": `{"X-Api-Key":"k","Accept":"a"}`}),
	}
	out := nodeclean.Rewrite(nodes, func(c nodeclean.Credential) string { return "{{secret.X}}" })
	if nodes[0].Config["password"] != "pw1" {
		t.Fatal("input modified")
	}
	if out[0].Config["password"] != "{{secret.X}}" {
		t.Fatalf("password = %v", out[0].Config["password"])
	}
	var h map[string]string
	if err := json.Unmarshal([]byte(out[0].Config["headers"].(string)), &h); err != nil {
		t.Fatal(err)
	}
	if h["X-Api-Key"] != "{{secret.X}}" || h["Accept"] != "a" {
		t.Fatalf("headers = %v", h)
	}
	if len(nodeclean.TypedCredentials(out)) != 0 {
		t.Fatal("a rewritten credential is still typed")
	}
}

func TestSecretNamesAreValidAndDistinct(t *testing.T) {
	nodes := []storage.WorkflowNode{
		step("api_lookup", map[string]any{"label": "Customer API", "username": "u", "password": "p",
			"headers": `{"X-Api-Key":"k"}`}),
	}
	seen := map[string]bool{}
	for _, c := range nodeclean.TypedCredentials(nodes) {
		name := nodeclean.SecretName("8f3a9c21-1111-2222-3333-444455556666", c)
		if !storage.ValidVHostSecretName(name) {
			t.Errorf("invalid secret name %q", name)
		}
		if seen[name] {
			t.Errorf("duplicate secret name %q", name)
		}
		seen[name] = true
	}
	if !seen["WF_8F3A9C21_CUSTOMER_API_PASSWORD"] {
		t.Errorf("names = %v", seen)
	}
}
