// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

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
