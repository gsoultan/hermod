// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package logic

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/transformer"

	"github.com/gsoultan/hermod/pkg/infra/evaluator"
)

func init() {
	transformer.Register("validator", &ValidatorTransformer{})
}

type ValidatorTransformer struct{}

func (t *ValidatorTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	schema, _ := config["schema"].(string)
	if schema == "" {
		return msg, nil
	}

	var rules map[string]string
	err := json.Unmarshal([]byte(schema), &rules)
	if err != nil {
		return nil, fmt.Errorf("failed to parse validation schema: %w", err)
	}

	for path, expectedType := range rules {
		val := evaluator.EvaluateField(msg, path)
		if val == nil {
			return nil, fmt.Errorf("validation failed: field %s is missing", path)
		}
		actualType := fmt.Sprintf("%T", val)
		if expectedType != "" && !strings.Contains(strings.ToLower(actualType), strings.ToLower(expectedType)) {
			return nil, fmt.Errorf("validation failed: field %s expected type %s, got %s", path, expectedType, actualType)
		}
	}

	return msg, nil
}
