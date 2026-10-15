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
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/transformer"

	"github.com/gsoultan/hermod/pkg/infra/evaluator"
)

func init() {
	transformer.Register("mapping", &MappingTransformer{})
}

type MappingTransformer struct{}

func (t *MappingTransformer) Prepare(config map[string]any) (map[string]any, error) {
	mappingStr, _ := config["mapping"].(string)
	if mappingStr != "" {
		var mapping map[string]any
		if err := json.Unmarshal([]byte(mappingStr), &mapping); err == nil {
			config["_parsed_mapping"] = mapping
		}
	}
	return config, nil
}

func (t *MappingTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}

	field, _ := config["field"].(string)
	var mapping map[string]any
	if cached, ok := config["_parsed_mapping"].(map[string]any); ok {
		mapping = cached
	} else {
		mappingStr, _ := config["mapping"].(string)
		_ = json.Unmarshal([]byte(mappingStr), &mapping)
	}

	mappingType, _ := config["mappingType"].(string) // "exact", "range", "regex"

	fieldValRaw := evaluator.EvaluateField(msg, field)
	fieldVal := fmt.Sprintf("%v", fieldValRaw)

	configuredTarget, _ := config["targetField"].(string)
	targetField, err := evaluator.OutputField(field, configuredTarget, "")
	if err != nil {
		return msg, err
	}

	switch mappingType {
	case "range":
		val, ok := evaluator.ToFloat64(fieldValRaw)
		if ok {
			for k, v := range mapping {
				if strings.Contains(k, "-") {
					parts := strings.Split(k, "-")
					if len(parts) == 2 {
						low, _ := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
						high, _ := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
						if val >= low && val <= high {
							msg.SetData(targetField, v)
							return msg, nil
						}
					}
				} else if before, ok0 := strings.CutSuffix(k, "+"); ok0 {
					low, _ := strconv.ParseFloat(before, 64)
					if val >= low {
						msg.SetData(targetField, v)
						return msg, nil
					}
				}
			}
		}
	case "regex":
		for k, v := range mapping {
			matched, _ := regexp.MatchString(k, fieldVal)
			if matched {
				msg.SetData(targetField, v)
				return msg, nil
			}
		}
	default:
		// exact (default)
		if newVal, ok := mapping[fieldVal]; ok {
			msg.SetData(targetField, newVal)
		}
	}
	return msg, nil
}
