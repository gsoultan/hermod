// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

package inference

import (
	"context"
	"fmt"
	"strings"
)

// predictMLflow calls an MLflow scoring server with the rows as
// "dataframe_records". MLflow answers {"predictions": [...]}, one entry per
// row: an object for a model with several outputs, a bare value otherwise. A
// bare value comes back as {"prediction": value}, so every prediction is a row.
func (c *Client) predictMLflow(ctx context.Context, t Target, rows []Row) ([]Row, error) {
	var reply struct {
		Predictions []any `json:"predictions"`
	}
	endpoint := strings.TrimRight(t.URL, "/") + "/invocations"
	if err := c.post(ctx, t, endpoint, map[string]any{"dataframe_records": rows}, &reply); err != nil {
		return nil, err
	}
	if len(reply.Predictions) != len(rows) {
		return nil, fmt.Errorf("the model server returned %d predictions for %d rows", len(reply.Predictions), len(rows))
	}
	out := make([]Row, len(rows))
	for i, p := range reply.Predictions {
		if obj, ok := p.(map[string]any); ok {
			out[i] = obj
			continue
		}
		out[i] = Row{"prediction": p}
	}
	return out, nil
}
