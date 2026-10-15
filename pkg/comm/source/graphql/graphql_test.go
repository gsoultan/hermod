// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package graphql

import (
	"testing"

	"github.com/gsoultan/hermod"
)

func TestGraphQLSourceRead(t *testing.T) {
	path := "/api/graphql/test"
	src := NewGraphQLSource(path)
	defer src.Close()

	msg := &mockMessage{id: "test-id"}

	go func() {
		_ = Dispatch(path, msg)
	}()

	readMsg, err := src.Read(t.Context())
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	if readMsg.ID() != "test-id" {
		t.Errorf("Expected ID test-id, got %s", readMsg.ID())
	}
}

type mockMessage struct {
	hermod.Message
	id string
}

func (m *mockMessage) ID() string                     { return m.id }
func (m *mockMessage) ToMap() map[string]any          { return nil }
func (m *mockMessage) MetadataRef() map[string]string { return nil }
func (m *mockMessage) DataRef() map[string]any        { return nil }
