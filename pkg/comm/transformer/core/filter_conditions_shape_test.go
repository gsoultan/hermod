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
	"testing"

	"github.com/gsoultan/hermod/pkg/comm/message"
)

// A Filter node's conditions arrive in whatever shape stored them. The editor
// saves a JSON string; a workflow created or imported through the API, and a
// JSONB round trip, hand over a list. The filter read only the string, so a
// list was "no conditions" and every record passed -- a filter that filters
// nothing, with no error. The If node already read every shape
// (evaluator.CheckConfigConditions); this holds Filter to the same.
func TestFilterReadsConditionsInEveryStoredShape(t *testing.T) {
	cond := map[string]any{"field": "array_len(source.users)", "operator": ">", "value": "1"}
	shapes := map[string]any{
		"json string": `[{"field":"array_len(source.users)","operator":">","value":"1"}]`,
		"[]any":       []any{cond},
		"[]map":       []map[string]any{cond},
	}
	for name, conditions := range shapes {
		for _, tc := range []struct {
			users []any
			kept  bool
		}{
			{[]any{1, 2}, true},
			{[]any{1}, false},
		} {
			t.Run(name, func(t *testing.T) {
				f := &FilterTransformer{}
				cfg, err := f.Prepare(map[string]any{"transType": "filter_data", "conditions": conditions})
				if err != nil {
					t.Fatalf("Prepare: %v", err)
				}
				m := message.AcquireMessage()
				t.Cleanup(func() { message.ReleaseMessage(m) })
				m.SetData("users", tc.users)
				out, err := f.Transform(t.Context(), m, cfg)
				if err != nil {
					t.Fatalf("Transform: %v", err)
				}
				if kept := out != nil; kept != tc.kept {
					t.Errorf("%d users: kept = %v, want %v", len(tc.users), kept, tc.kept)
				}
			})
		}
	}
}
