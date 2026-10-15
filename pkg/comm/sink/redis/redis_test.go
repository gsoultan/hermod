// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

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
