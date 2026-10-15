// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

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
