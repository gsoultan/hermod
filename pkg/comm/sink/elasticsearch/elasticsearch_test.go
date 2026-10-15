// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

package elasticsearch

import (
	"testing"

	"github.com/gsoultan/hermod/pkg/comm/message"
)

func TestElasticsearchSink_renderIndex(t *testing.T) {
	s := &ElasticsearchSink{
		index: "logs-{{.table}}",
	}

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	msg.SetTable("users")

	index, err := s.renderIndex(msg)
	if err != nil {
		t.Fatalf("failed to render index: %v", err)
	}

	if index != "logs-users" {
		t.Errorf("expected logs-users, got %s", index)
	}
}

func TestElasticsearchSink_renderIndex_Nested(t *testing.T) {
	s := &ElasticsearchSink{
		index: "idx-{{.after.category}}",
	}

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	// Realistic CDC-like payload
	msg.SetAfter([]byte(`{"after": {"category": "test"}}`))

	index, err := s.renderIndex(msg)
	if err != nil {
		t.Fatalf("failed to render index: %v", err)
	}

	if index != "idx-test" {
		t.Errorf("expected idx-test, got %s", index)
	}
}

func TestElasticsearchSink_renderIndex_Static(t *testing.T) {
	s := &ElasticsearchSink{
		index: "static-index",
	}

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)

	index, err := s.renderIndex(msg)
	if err != nil {
		t.Fatalf("failed to render index: %v", err)
	}

	if index != "static-index" {
		t.Errorf("expected static-index, got %s", index)
	}
}
