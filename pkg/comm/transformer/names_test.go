// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

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
