// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

package interfaces

import (
	"context"
	"slices"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
)

type nopExecutor struct{}

func (nopExecutor) Execute(context.Context, NodeContext, string, *storage.WorkflowNode, hermod.Message) ([]hermod.Message, string, error) {
	return nil, "", nil
}

func TestNodeExecutorTypesListsRegisteredTypesSorted(t *testing.T) {
	RegisterNodeExecutor("types_test_b", nopExecutor{})
	RegisterNodeExecutor("types_test_a", nopExecutor{})

	got := NodeExecutorTypes()
	if !slices.IsSorted(got) {
		t.Errorf("not sorted: %v", got)
	}
	for _, want := range []string{"types_test_a", "types_test_b"} {
		if !slices.Contains(got, want) {
			t.Errorf("%s is registered and not listed: %v", want, got)
		}
	}
}
