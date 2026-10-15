// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

//go:build integration
// +build integration

package redis

import (
	"os"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

func TestRedisSink_Write(t *testing.T) {
	if os.Getenv("HERMOD_INTEGRATION") != "1" {
		t.Skip("skipping integration test; set HERMOD_INTEGRATION=1 to run")
	}
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		t.Skip("integration test: set REDIS_ADDR to run")
	}

	password := os.Getenv("REDIS_PASSWORD")
	stream := os.Getenv("REDIS_STREAM")
	if stream == "" {
		stream = "cdc-stream"
	}

	snk, err := NewRedisSink(addr, password, stream, nil)
	if err != nil {
		t.Fatalf("failed to create RedisSink: %v", err)
	}
	defer snk.Close()

	msg := message.AcquireMessage()
	msg.SetID("test-1")
	msg.SetOperation(hermod.OpCreate)
	msg.SetTable("users")
	msg.SetSchema("public")
	msg.SetAfter([]byte(`{"id": 1}`))

	err = snk.Write(t.Context(), msg)
	if err != nil {
		t.Errorf("failed to write to RedisSink: %v", err)
	}
}
