// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

package sse

import (
	"context"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/sse"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

func TestSSESink_PublishAndReceive(t *testing.T) {
	stream := "test-stream"
	sink := NewSSESink(stream, nil)

	// Subscribe first to avoid race
	ch, unsub := sse.GetDataHub().Subscribe(stream, 1)
	defer unsub()

	dm := message.AcquireMessage()
	dm.SetID("id-1")
	dm.SetOperation(hermod.OpCreate)
	dm.SetData("hello", "world")

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	if err := sink.Write(ctx, dm); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	select {
	case ev := <-ch:
		if string(ev.Data) == "" {
			t.Fatalf("expected data, got empty")
		}
		if ev.ID != "id-1" {
			t.Fatalf("unexpected id: %s", ev.ID)
		}
	case <-ctx.Done():
		t.Fatalf("timeout waiting for event")
	}
}

func TestSSESink_WithSecurity(t *testing.T) {
	stream := "secure-stream"
	token := "secret-token"
	origins := []string{"http://localhost:3000"}

	_ = NewSSESink(stream, nil).WithSecurity(token, origins)

	cfg, ok := sse.GetDataHub().GetStreamConfig(stream)
	if !ok {
		t.Fatal("config not found in hub")
	}

	if cfg.AuthToken != token {
		t.Errorf("expected token %s, got %s", token, cfg.AuthToken)
	}

	if len(cfg.AllowedOrigins) != 1 || cfg.AllowedOrigins[0] != origins[0] {
		t.Errorf("expected origins %v, got %v", origins, cfg.AllowedOrigins)
	}
}
