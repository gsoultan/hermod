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
