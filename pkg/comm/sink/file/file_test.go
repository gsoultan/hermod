// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

package file

import (
	"os"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/pkg/comm/formatter/json"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

func TestFileSink(t *testing.T) {
	tmpfile, err := os.CreateTemp("", "hermod-test-*.log")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())
	tmpfile.Close()

	formatter := json.NewJSONFormatter()
	sink, err := NewFileSink(tmpfile.Name(), formatter)
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()

	msg := message.AcquireMessage()
	msg.SetID("test-id")
	msg.SetTable("users")
	msg.SetSchema("public")
	msg.SetAfter([]byte(`{"name":"john"}`))

	err = sink.Write(t.Context(), msg)
	if err != nil {
		t.Fatalf("failed to write message: %v", err)
	}

	content, err := os.ReadFile(tmpfile.Name())
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(content), "test-id") {
		t.Errorf("expected content to contain test-id, got %s", string(content))
	}
	if !strings.Contains(string(content), "users") {
		t.Errorf("expected content to contain users, got %s", string(content))
	}
}
