// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package evaluator

import (
	"strings"
	"testing"

	"github.com/gsoultan/hermod/pkg/comm/message"
)

// A condition node read its conditions with ParseConditions on every message.
// For the single field/operator/value form the editor writes most often, that
// built a fresh map per message — 10% of every allocation in
// BenchmarkWorkflowThroughput — to answer a question whose shape never
// changes. CheckConfigConditions answers from the config without building a
// list the caller could see, so nothing shared escapes to be mutated either.

func TestCheckConfigConditionsAgreesWithParseAndEvaluate(t *testing.T) {
	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	msg.SetData("status", "active")
	msg.SetData("amount", 42.0)

	configs := []map[string]any{
		{},
		{"field": "status", "operator": "=", "value": "active"},
		{"field": "status", "operator": "=", "value": "closed"},
		{"field": "amount", "operator": ">", "value": "40"},
		{"field": "status", "operator": "regex", "value": "^act"},
		{"field": "status", "operator": "regex", "value": "{{.status}}"},
		{"conditions": `[{"field":"status","operator":"=","value":"active"},{"field":"amount","operator":"<","value":"50"}]`},
		{"conditions": `[{"field":"status","operator":"=","value":"active"},{"field":"amount","operator":">","value":"50"}]`},
		{"conditions": []any{map[string]any{"field": "status", "operator": "contains", "value": "tiv"}}},
		{"conditions": []map[string]any{{"field": "status", "operator": "!=", "value": "active"}}},
	}
	for i, cfg := range configs {
		want := EvaluateConditions(msg, ParseConditions(cfg))
		got, err := CheckConfigConditions(msg, cfg)
		if err != nil {
			t.Errorf("config %d: unexpected error %v", i, err)
		}
		if got != want {
			t.Errorf("config %d %v: CheckConfigConditions = %v, ParseConditions+EvaluateConditions = %v", i, cfg, got, want)
		}
	}
}

func TestCheckConfigConditionsRefusesAnUncompilablePattern(t *testing.T) {
	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	msg.SetData("status", "active")

	for _, cfg := range []map[string]any{
		{"field": "status", "operator": "regex", "value": "[unclosed"},
		{"conditions": `[{"field":"status","operator":"not_regex","value":"[unclosed"}]`},
	} {
		_, err := CheckConfigConditions(msg, cfg)
		if err == nil || !strings.Contains(err.Error(), "[unclosed") {
			t.Errorf("%v: err = %v, want one naming the pattern", cfg, err)
		}
	}
}

func TestCheckConfigConditionsBuildsNothingPerMessage(t *testing.T) {
	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	msg.SetData("status", "active")

	for _, cfg := range []map[string]any{
		{"field": "status", "operator": "=", "value": "active"},
		{"conditions": `[{"field":"status","operator":"=","value":"active"}]`},
	} {
		conds := ParseConditions(cfg)
		evalOnly := testing.AllocsPerRun(500, func() { _ = EvaluateConditions(msg, conds) })
		whole := testing.AllocsPerRun(500, func() { _, _ = CheckConfigConditions(msg, cfg) })
		if whole > evalOnly {
			t.Errorf("%v: %.1f allocations per check, %.1f of them reading the config; evaluating alone takes %.1f",
				cfg, whole, whole-evalOnly, evalOnly)
		}
	}
}
