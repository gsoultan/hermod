// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

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
