// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package advanced

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/gsoultan/hermod/pkg/comm/transformer"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
)

func init() {
	adv := &AdvancedTransformer{evaluator: evaluator.NewEvaluator()}
	transformer.Register("advanced", adv)
	transformer.Register("set", adv)
}

type columnConfig struct {
	path string
	expr any
}

type AdvancedTransformer struct {
	evaluator *evaluator.Evaluator
}

// parseColumns collects the `column.<path>` entries of a config into the order
// they are applied in.
//
// The sort is the point. Ranging a Go map is randomised per range, and a `set`
// node applies its columns one at a time, so two columns touching the same path
// -- or one whose expression reads what another just wrote -- resolved
// differently from message to message within a single run, with nothing wrong
// in the config and nothing in the logs. The editor's row order cannot be used:
// the config is stored as JSON, which has no key order, so by the time it is
// back in a map[string]any the row order is already gone. Sorting by path is
// what is left, and it puts a parent before the child that writes into it.
func parseColumns(config map[string]any) []columnConfig {
	var columns []columnConfig
	for k, v := range config {
		if after, ok := strings.CutPrefix(k, "column."); ok {
			columns = append(columns, columnConfig{
				path: after,
				expr: v,
			})
		}
	}
	slices.SortFunc(columns, func(a, b columnConfig) int {
		return strings.Compare(a.path, b.path)
	})
	return columns
}

func (t *AdvancedTransformer) Prepare(config map[string]any) (map[string]any, error) {
	if columns := parseColumns(config); len(columns) > 0 {
		config["_parsed_columns"] = columns
	}
	return config, nil
}

func (t *AdvancedTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}

	transType, _ := config["transType"].(string)

	var columns []columnConfig
	if cached, ok := config["_parsed_columns"].([]columnConfig); ok {
		columns = cached
	} else {
		// Fallback for non-prepared config -- the preview endpoint takes it.
		// Shares parseColumns with Prepare so a node cannot resolve one way in
		// the engine and another in the editor's preview.
		columns = parseColumns(config)
	}

	if transType == "advanced" {
		results := make(map[string]any)
		for _, col := range columns {
			result := t.evaluator.EvaluateAdvancedExpression(msg, col.expr)
			if result != nil {
				results[col.path] = result
			}
		}

		// Written in the order they were evaluated in, not by ranging `results`.
		// Ranging it randomised the write order, and SetData nests a dotted path,
		// so two columns whose paths overlap -- `a` and `a.b` -- produced two
		// different messages from the same node and the same input. Measured on
		// one config over 300 runs: 266 came out `{"a":{"b":"child"}}` and 34
		// came out `{"a":"parent"}`.
		msg.ClearPayloads()
		for _, col := range columns {
			if result, ok := results[col.path]; ok {
				msg.SetData(col.path, result)
			}
		}
	} else { // "set"
		for _, col := range columns {
			result := t.evaluator.EvaluateAdvancedExpression(msg, col.expr)
			if replaceAfterImage(msg, col.path, result) {
				continue
			}
			msg.SetData(col.path, result)
		}
	}

	return msg, nil
}

// replaceAfterImage makes `column.after = <object>` on a CDC message replace the
// row, which is what a foreach followed by `column.after = source.after._item`
// means. Written through SetData it became a literal "after" key beside the old
// columns, and that key alone serialises as the after-image, so every field a
// later node wrote -- a db_lookup's targetField -- landed beside it and never
// reached the next node or the sink. A row with a real "after" column keeps the
// SetData behaviour, which writes into that column.
func replaceAfterImage(msg hermod.Message, path string, value any) bool {
	row, ok := value.(map[string]any)
	setter, canSet := msg.(interface{ SetAfter([]byte) })
	if !ok || !canSet || msg.Operation() == "" || !strings.EqualFold(path, "after") {
		return false
	}
	if _, column := msg.Data()["after"]; column {
		return false
	}
	b, err := json.Marshal(row)
	if err != nil {
		return false
	}
	setter.SetAfter(b)
	return true
}
