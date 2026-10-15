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
	"strings"
	"testing"

	"github.com/gsoultan/hermod/pkg/comm/message"
)

func aggregate(t *testing.T, cfg map[string]any) (map[string]any, error) {
	t.Helper()
	m := message.AcquireMessage()
	m.SetData("amount", "12")
	out, err := (&AggregateTransformer{}).Transform(t.Context(), m, cfg)
	if err != nil {
		return nil, err
	}
	return out.Data(), nil
}

func TestAggregate_ACallWritesToItsTargetField(t *testing.T) {
	data, err := aggregate(t, map[string]any{"field": "toint(source.amount)", "type": "sum", "targetField": "amount_total"})
	if err != nil {
		t.Fatal(err)
	}
	if data["amount_total"] != 12.0 || len(data) != 2 {
		t.Errorf("got %v, want amount_total=12 beside amount and nothing else", data)
	}
}

// It wrote the sum to "toint(source.amount)_sum", which SetData splits at its
// dot.
func TestAggregate_ACallWithNoTargetFieldIsRefused(t *testing.T) {
	data, err := aggregate(t, map[string]any{"field": "toint(source.amount)", "type": "sum"})
	if err == nil {
		t.Fatalf("no error; the message now holds %v", data)
	}
	if !strings.Contains(err.Error(), "target field") {
		t.Errorf("error %q should say to set a target field", err)
	}
}

// The grouping key is only read, so a call there needs nothing else set.
func TestAggregate_ACallAsTheGroupingKeyNeedsNoTargetField(t *testing.T) {
	data, err := aggregate(t, map[string]any{"field": "amount", "type": "sum", "groupBy": "lower(source.region)"})
	if err != nil {
		t.Fatal(err)
	}
	if data["amount_sum"] != 12.0 {
		t.Errorf("got %v, want amount_sum=12", data)
	}
}
