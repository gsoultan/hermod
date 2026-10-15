// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

package ai

import (
	"strings"
	"testing"

	"github.com/gsoultan/hermod/pkg/comm/message"
)

func extractTerms(t *testing.T, cfg map[string]any) (map[string]any, error) {
	t.Helper()
	m := message.AcquireMessage()
	m.SetData("note", "  Urgent Refund  ")
	out, err := (&TermExtractionTransformer{}).Transform(t.Context(), m, cfg)
	if err != nil {
		return nil, err
	}
	return out.Data(), nil
}

func TestTermExtraction_ACallWritesToItsTargetField(t *testing.T) {
	data, err := extractTerms(t, map[string]any{"field": "trim(source.note)", "targetField": "note_terms"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := data["note_terms"]; !ok || len(data) != 2 {
		t.Errorf("got %v, want note_terms beside note and nothing else", data)
	}
}

// It wrote the terms to "trim(source.note)_terms", which SetData splits at
// its dot.
func TestTermExtraction_ACallWithNoTargetFieldIsRefused(t *testing.T) {
	data, err := extractTerms(t, map[string]any{"field": "trim(source.note)"})
	if err == nil {
		t.Fatalf("no error; the message now holds %v", data)
	}
	if !strings.Contains(err.Error(), "target field") {
		t.Errorf("error %q should say to set a target field", err)
	}
}

func TestTermExtraction_APlainFieldKeepsItsName(t *testing.T) {
	data, err := extractTerms(t, map[string]any{"field": "note"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := data["note_terms"]; !ok || len(data) != 2 {
		t.Errorf("got %v, want note_terms beside note", data)
	}
}
