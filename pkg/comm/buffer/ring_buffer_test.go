// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

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
