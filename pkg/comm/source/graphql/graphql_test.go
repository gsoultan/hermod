// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

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
