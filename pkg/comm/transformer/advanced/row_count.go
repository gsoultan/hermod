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
	"strconv"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/transformer"
)

func init() {
	transformer.Register("row_count", &RowCountTransformer{})
}

type RowCountTransformer struct{}

func (t *RowCountTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}

	variableName, _ := config["variableName"].(string)
	if variableName == "" {
		variableName = "row_count"
	}

	workflowID, _ := ctx.Value(hermod.WorkflowIDKey).(string)
	nodeID, _ := ctx.Value(hermod.NodeIDKey).(string)
	stateKey := fmt.Sprintf("rowcount:%s:%s:%s", workflowID, nodeID, variableName)

	var store hermod.StateStore
	if s, ok := ctx.Value(hermod.StateStoreKey).(hermod.StateStore); ok {
		store = s
	}

	if store != nil {
		count := int64(0)
		data, err := store.Get(ctx, stateKey)
		if err == nil && data != nil {
			count, _ = strconv.ParseInt(string(data), 10, 64)
		}
		count++
		_ = store.Set(ctx, stateKey, []byte(strconv.FormatInt(count, 10)))

		// Also inject into message if requested
		if asField, _ := config["asField"].(bool); asField {
			msg.SetData(variableName, count)
		}
	}

	return msg, nil
}
