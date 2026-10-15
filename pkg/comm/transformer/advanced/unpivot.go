// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

package advanced

import (
	"context"
	"fmt"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/transformer"

	"github.com/gsoultan/hermod/pkg/infra/evaluator"
)

func init() {
	transformer.Register("unpivot", &UnpivotTransformer{})
}

type UnpivotTransformer struct{}

func (t *UnpivotTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}

	pivotColumns, _ := config["pivotColumns"].([]any)
	if len(pivotColumns) == 0 {
		return msg, nil
	}

	attributeField, _ := config["attributeField"].(string)
	if attributeField == "" {
		attributeField = "attribute"
	}

	valueField, _ := config["valueField"].(string)
	if valueField == "" {
		valueField = "value"
	}

	resultField, _ := config["resultField"].(string)
	if resultField == "" {
		resultField = "_fanout"
	}

	data := msg.Data()
	unpivoted := make([]any, 0, len(pivotColumns))

	for _, colRaw := range pivotColumns {
		col := fmt.Sprintf("%v", colRaw)
		val := evaluator.GetValByPath(data, col)

		if val == nil {
			continue
		}

		// Create a new record
		newRecord := make(map[string]any)
		for k, v := range data {
			// Skip pivot columns in the new records?
			// Usually yes, we want them removed from the base and added as attribute/value.
			isPivotCol := false
			for _, pc := range pivotColumns {
				if fmt.Sprintf("%v", pc) == k {
					isPivotCol = true
					break
				}
			}
			if !isPivotCol {
				newRecord[k] = v
			}
		}
		newRecord[attributeField] = col
		newRecord[valueField] = val

		unpivoted = append(unpivoted, newRecord)
	}

	msg.SetData(resultField, unpivoted)
	return msg, nil
}
