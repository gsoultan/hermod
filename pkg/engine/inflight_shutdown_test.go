// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package engine

import (
	"context"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// A message handed to the worker pool after the workers have gone must not
// keep the engine from stopping.
//
// The buffer-to-sink loop counts a message in flight once it is queued for a
// worker, and the worker that processes it counts it out. Workers used to leave
// on cancellation, so a message queued in that window — the buffer's handler
// races the cancelled context, and Go picks among ready cases at random — had
// no worker left to count it out. Start then waited on the in-flight count for
// ever. CI saw it as "the engine did not stop" in the gRPC source tests, a few
// runs in a hundred under load. Workers now leave when their queues close.

// lateBuffer hands over one message only after the engine is stopping and its
// workers have had time to leave.
type lateBuffer struct{}

func (lateBuffer) Produce(ctx context.Context, msg hermod.Message) error {
	<-ctx.Done()
	return ctx.Err()
}

func (lateBuffer) Consume(ctx context.Context, handler hermod.Handler) error {
	<-ctx.Done()
	time.Sleep(50 * time.Millisecond)
	msg := message.AcquireMessage()
	msg.SetID("late-1")
	_ = handler(context.Background(), msg)
	return nil
}

func (lateBuffer) Close() error { return nil }

func TestAMessageQueuedAfterTheWorkersLeftDoesNotHoldUpStop(t *testing.T) {
	eng := NewEngine(idleSource{}, []hermod.Sink{&mockSink{}}, lateBuffer{})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = eng.Start(ctx)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the engine did not stop: a message queued after its workers left is still counted in flight")
	}
}
