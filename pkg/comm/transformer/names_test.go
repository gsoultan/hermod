// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

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
