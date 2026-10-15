// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package control

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	msgpkg "github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
)

type switchStubCtx struct {
	stubCtx
}

func (s *switchStubCtx) EvaluateConditions(msg hermod.Message, conditions []map[string]any) bool {
	return evaluator.EvaluateConditions(msg, conditions)
}

func TestSwitch_Execute_Regex(t *testing.T) {
	n := &SwitchNode{}
	cases := []map[string]any{
		{"label": "match", "operator": "regex", "value": "^active.*"},
		{"label": "other", "operator": "=", "value": "something"},
	}
	casesJSON, _ := json.Marshal(cases)
	node := &storage.WorkflowNode{
		Config: map[string]any{
			"field": "status",
			"cases": string(casesJSON),
		},
	}

	m := msgpkg.AcquireMessage()
	defer msgpkg.ReleaseMessage(m)
	m.SetData("status", "active_session")

	msgs, branch, err := n.Execute(context.Background(), &switchStubCtx{}, "wf1", node, m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if branch != "match" {
		t.Errorf("expected branch match, got %q", branch)
	}
	if len(msgs) != 1 {
		t.Errorf("expected 1 message, got %d", len(msgs))
	}
}

func TestSwitch_Execute_Function(t *testing.T) {
	n := &SwitchNode{}
	cases := []map[string]any{
		{"label": "match", "operator": "=", "value": "active"},
	}
	casesJSON, _ := json.Marshal(cases)
	node := &storage.WorkflowNode{
		Config: map[string]any{
			"field": "lower(source.status)",
			"cases": string(casesJSON),
		},
	}

	m := msgpkg.AcquireMessage()
	defer msgpkg.ReleaseMessage(m)
	m.SetData("status", "ACTIVE")

	_, branch, err := n.Execute(context.Background(), &switchStubCtx{}, "wf1", node, m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if branch != "match" {
		t.Errorf("expected branch match, got %q", branch)
	}
}

func TestSwitch_Execute_Default(t *testing.T) {
	n := &SwitchNode{}
	cases := []map[string]any{
		{"label": "match", "operator": "=", "value": "active"},
	}
	casesJSON, _ := json.Marshal(cases)
	node := &storage.WorkflowNode{
		Config: map[string]any{
			"field": "status",
			"cases": string(casesJSON),
		},
	}

	m := msgpkg.AcquireMessage()
	defer msgpkg.ReleaseMessage(m)
	m.SetData("status", "inactive")

	_, branch, err := n.Execute(context.Background(), &switchStubCtx{}, "wf1", node, m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if branch != "default" {
		t.Errorf("expected branch default, got %q", branch)
	}
}

// TestSwitch_Execute_CasesSavedAsArray pins the shape the editor actually
// saves. SwitchConfig.tsx writes `cases` straight into node.data as a JSON
// array -- it never stringifies the way RouterEditor.tsx does -- so every
// workflow built in the UI reaches the engine with []any under "cases".
// Reading only the string form made the type assertion fail, leaving an empty
// case list, and a switch with perfectly good numeric cases fell through to
// "default" on every message.
func TestSwitch_Execute_CasesSavedAsArray(t *testing.T) {
	for _, tc := range []struct {
		name   string
		amount any
		want   string
	}{
		{"greater-than wins", 150, "high"},
		{"equals wins", 100, "exact"},
		{"no case matches", 10, "default"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := &SwitchNode{}
			node := &storage.WorkflowNode{
				Config: map[string]any{
					"field": "amount",
					// Exactly what a UI-built workflow round trips through
					// JSONB: a slice of maps, not a string.
					"cases": []any{
						map[string]any{"label": "high", "operator": ">", "value": "100"},
						map[string]any{"label": "exact", "operator": "=", "value": "100"},
					},
				},
			}

			m := msgpkg.AcquireMessage()
			defer msgpkg.ReleaseMessage(m)
			m.SetData("amount", tc.amount)

			_, branch, err := n.Execute(context.Background(), &switchStubCtx{}, "wf1", node, m)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if branch != tc.want {
				t.Errorf("amount %v: expected branch %q, got %q", tc.amount, tc.want, branch)
			}
		})
	}
}

// The editor stores a case value picked from the record's fields as a
// {{path}} reference. The case compares the switch field against that
// field's value in the same message, not against the literal text.
func TestSwitch_Execute_ValueFromField(t *testing.T) {
	cases := []map[string]any{
		{"label": "over_limit", "operator": ">", "value": "{{limit}}"},
		{"label": "same_region", "operator": "=", "value": "{{customer.region}}"},
	}
	casesJSON, _ := json.Marshal(cases)
	node := &storage.WorkflowNode{
		Config: map[string]any{
			"field": "amount",
			"cases": string(casesJSON),
		},
	}

	tests := []struct {
		name   string
		amount any
		limit  any
		region any
		want   string
	}{
		{name: "amount above the record's limit", amount: 150, limit: 100, region: "eu", want: "over_limit"},
		{name: "amount equals another field", amount: "eu", limit: "zz", region: "eu", want: "same_region"},
		{name: "no case matches", amount: 50, limit: 100, region: "eu", want: "default"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := msgpkg.AcquireMessage()
			defer msgpkg.ReleaseMessage(m)
			m.SetData("amount", tt.amount)
			m.SetData("limit", tt.limit)
			m.SetData("customer", map[string]any{"region": tt.region})

			_, branch, err := (&SwitchNode{}).Execute(context.Background(), &switchStubCtx{}, "wf1", node, m)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if branch != tt.want {
				t.Errorf("branch = %q, want %q", branch, tt.want)
			}
		})
	}
}

// The editor's function picker wraps either side in a one-argument call:
// lower(source.status) as the switch field, and {{lower(source.x)}} or
// {{upper("eu")}} as a case value.
func TestSwitch_Execute_FunctionOnBothSides(t *testing.T) {
	cases := []map[string]any{
		{"label": "from_field", "operator": "=", "value": "{{lower(source.expected)}}"},
		{"label": "from_text", "operator": "=", "value": "{{lower(\"PAUSED\")}}"},
	}
	casesJSON, _ := json.Marshal(cases)
	node := &storage.WorkflowNode{
		Config: map[string]any{
			"field": "lower(source.status)",
			"cases": string(casesJSON),
		},
	}

	tests := []struct {
		status string
		want   string
	}{
		{status: "ACTIVE", want: "from_field"},
		{status: "Paused", want: "from_text"},
		{status: "closed", want: "default"},
	}
	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			m := msgpkg.AcquireMessage()
			defer msgpkg.ReleaseMessage(m)
			m.SetData("status", tt.status)
			m.SetData("expected", "Active")

			_, branch, err := (&SwitchNode{}).Execute(context.Background(), &switchStubCtx{}, "wf1", node, m)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if branch != tt.want {
				t.Errorf("branch = %q, want %q", branch, tt.want)
			}
		})
	}
}
