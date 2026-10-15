// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package registry

import (
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// Every transformation setting takes {{secret:NAME}} and {{env("NAME")}}, for
// the workflow's vhost -- not only the fields that happen to be templates. A
// Set value is one: {{secret:NAME}} used to be read there as a field and
// write "".
func TestTransformationSettingsResolveSecretAndEnvReferences(t *testing.T) {
	t.Setenv("HERMOD_ENV_REGION", "eu-west-1")
	reg, _ := newSecretRegistry(t)

	for expr, want := range map[string]any{
		"{{secret:API_KEY}}":                "b-key",
		`key-{{secret("ONLY_B")}}`:          "key-b-only",
		`{{env("REGION")}}`:                 "eu-west-1",
		"{{env:REGION}}/{{secret:API_KEY}}": "eu-west-1/b-key",
		"{{env.REGION}}/{{secret.API_KEY}}": "eu-west-1/b-key",
		// A whole Set value, as the editor writes it beside source.x.
		"secret.API_KEY": "b-key",
		"env.REGION":     "eu-west-1",
	} {
		if got := previewSecret(t, reg, "tenant-b", expr); got != want {
			t.Errorf("%s wrote %#v, want %#v", expr, got, want)
		}
	}
}

// A reference the workflow's vhost cannot answer fails the step, naming the
// setting and the reference, rather than running with the token as a key.
func TestATransformationWithAnUnresolvedReferenceFails(t *testing.T) {
	reg, _ := newSecretRegistry(t)
	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetVHost("tenant-a")
	_, err := reg.TestTransformationPipeline(t.Context(), []storage.Transformation{{
		Type:   "set",
		Config: map[string]any{"transType": "set", "column.out": "{{secret:ONLY_B}}"},
	}}, msg)
	if err == nil || !strings.Contains(err.Error(), `secret "ONLY_B"`) || !strings.Contains(err.Error(), "column.out") {
		t.Fatalf("err = %v, want one naming the setting and the reference", err)
	}
}

// Workflow nodes that are not transformations -- a condition here -- read
// references in their settings too, and the stored node is not modified.
func TestWorkflowNodeSettingsResolveReferences(t *testing.T) {
	reg, _ := newSecretRegistry(t)
	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetVHost("tenant-a")
	msg.SetData("key", "a-key")

	node := &storage.WorkflowNode{ID: "c1", Type: "condition", Config: map[string]any{
		"field": "key", "operator": "=", "value": "{{secret:API_KEY}}",
	}}
	out, branch, err := reg.runWorkflowNode(t.Context(), "wf", node, msg)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range out {
		m.Release()
	}
	if branch != "true" {
		t.Errorf("branch = %q, want true: the condition compared against the token, not the secret", branch)
	}
	if node.Config["value"] != "{{secret:API_KEY}}" {
		t.Errorf("the stored node was modified: %v", node.Config["value"])
	}
}

// A SQL template binds every token as a parameter; a secret is never spliced
// into its text.
func TestSQLTemplatesAreNotExpanded(t *testing.T) {
	for _, k := range []string{"queryTemplate", "whereClause", "query", "queries", "steps", "_parsed_steps"} {
		if !skipNodeSetting(k) {
			t.Errorf("%s is expanded", k)
		}
	}
	for _, k := range []string{"apiKey", "key", "endpoint", "value", "column.out"} {
		if skipNodeSetting(k) {
			t.Errorf("%s is skipped", k)
		}
	}
}
