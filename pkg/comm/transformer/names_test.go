// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package transformer

import (
	"context"
	"slices"
	"testing"

	"github.com/gsoultan/hermod"
)

type nopTransformer struct{}

func (nopTransformer) Transform(_ context.Context, m hermod.Message, _ map[string]any) (hermod.Message, error) {
	return m, nil
}

func TestNamesListsWhatIsRegisteredSorted(t *testing.T) {
	r := NewRegistry()
	r.Register("zeta", nopTransformer{})
	r.Register("alpha", nopTransformer{})

	if got := r.Names(); !slices.Equal(got, []string{"alpha", "zeta"}) {
		t.Fatalf("Names() = %v", got)
	}

	Register("names_test_probe", nopTransformer{})
	if !slices.Contains(Names(), "names_test_probe") {
		t.Fatalf("the default registry does not list what was registered: %v", Names())
	}
}
