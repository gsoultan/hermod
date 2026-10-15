// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

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
