// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package core

import (
	"context"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/transformer"

	"github.com/gsoultan/hermod/pkg/infra/evaluator"
)

func init() {
	f := &FilterTransformer{}
	transformer.Register("filter_data", f)
	transformer.Register("validate", f)
}

type FilterTransformer struct{}

func (t *FilterTransformer) Prepare(config map[string]any) (map[string]any, error) {
	// ParseConditions reads every shape a config can carry them in: the JSON
	// string the editor saves, the list an API or import writes, and the single
	// field/operator/value of older workflows. Reading only the string made a
	// list "no conditions", so every record passed.
	if conditions := evaluator.ParseConditions(config); len(conditions) > 0 {
		config["_parsed_conditions"] = conditions
	}
	return config, nil
}

func (t *FilterTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}

	transType, _ := config["transType"].(string)

	conditions, cached := config["_parsed_conditions"].([]map[string]any)
	if !cached {
		conditions = evaluator.ParseConditions(config)
	}

	isValid := evaluator.EvaluateConditions(msg, conditions)
	asField := evaluator.ToBool(config["asField"]) || transType == "validate"

	if asField {
		targetField, _ := config["targetField"].(string)
		if targetField == "" {
			targetField = "is_valid"
		}
		msg.SetData(targetField, isValid)
		return msg, nil
	}

	if isValid {
		return msg, nil
	}
	return nil, nil // Filtered
}
