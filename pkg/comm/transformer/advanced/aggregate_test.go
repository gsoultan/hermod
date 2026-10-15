// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
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
	"testing"
	"time"

	"github.com/gsoultan/hermod/pkg/comm/message"
)

func TestAggregateTransformer_Tumbling(t *testing.T) {
	tr := &AggregateTransformer{}
	ctx := t.Context()

	config := map[string]any{
		"field":      "price",
		"type":       "sum",
		"window":     "1s",
		"windowType": "tumbling",
	}

	// First message
	msg1 := message.AcquireMessage()
	msg1.SetData("price", 10.0)
	_, _ = tr.Transform(ctx, msg1, config)
	if msg1.Data()["price_sum"].(float64) != 10.0 {
		t.Errorf("expected 10.0, got %v", msg1.Data()["price_sum"])
	}

	// Second message in same window
	msg2 := message.AcquireMessage()
	msg2.SetData("price", 20.0)
	_, _ = tr.Transform(ctx, msg2, config)
	if msg2.Data()["price_sum"].(float64) != 30.0 {
		t.Errorf("expected 30.0, got %v", msg2.Data()["price_sum"])
	}

	// Wait for next window
	time.Sleep(1100 * time.Millisecond)

	// Third message in new window
	msg3 := message.AcquireMessage()
	msg3.SetData("price", 5.0)
	_, _ = tr.Transform(ctx, msg3, config)
	if msg3.Data()["price_sum"].(float64) != 5.0 {
		t.Errorf("expected 5.0, got %v", msg3.Data()["price_sum"])
	}
}
