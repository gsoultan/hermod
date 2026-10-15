// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

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
