// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

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
