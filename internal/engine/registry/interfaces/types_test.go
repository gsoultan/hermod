// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

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
