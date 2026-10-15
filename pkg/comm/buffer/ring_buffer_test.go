// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package buffer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

func TestRingBuffer(t *testing.T) {
	rb := NewRingBuffer(10)
	ctx, cancel := context.WithTimeout(t.Context(), 1*time.Second)
	defer cancel()

	msg := message.AcquireMessage()
	msg.SetID("1")
	msg.SetPayload([]byte("hello"))

	go func() {
		err := rb.Produce(ctx, msg)
		if err != nil {
			t.Errorf("failed to produce: %v", err)
		}
	}()

	handler := func(ctx context.Context, m hermod.Message) error {
		if m.ID() != "1" {
			t.Errorf("expected ID 1, got %s", m.ID())
		}
		if string(m.Payload()) != "hello" {
			t.Errorf("expected payload hello, got %s", string(m.Payload()))
		}
		cancel() // Stop consuming after receiving the message
		return nil
	}

	err := rb.Consume(ctx, handler)
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Errorf("consume failed: %v", err)
	}

	rb.Close()
	message.ReleaseMessage(msg)
}
