// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package registry

import (
	"testing"
	"time"

	"github.com/gsoultan/hermod/pkg/comm/message"
)

// A debugger may answer a pause the moment it is announced. The pause must be
// answerable by then, or the answer is dropped and the message stays held
// until debuggerPauseLimit.
func TestAPauseCanBeAnsweredAsSoonAsItIsAnnounced(t *testing.T) {
	for i := range 300 {
		r := NewRegistry(nil)
		events := r.SubscribeBreakpoints("wf")

		msg := message.AcquireMessage()
		msg.SetID("m")
		released := make(chan struct{})
		go func() {
			r.PauseAtEdge(t.Context(), "wf", "e", "n", msg)
			close(released)
		}()

		ev := <-events
		r.DebuggerCommand("wf", ev.PauseID, "resume")
		select {
		case <-released:
		case <-time.After(2 * time.Second):
			t.Fatalf("run %d: a resume sent as the pause was announced was lost", i)
		}
		r.UnsubscribeDebugger("wf", events)
		r.Close()
		msg.Release()
	}
}

// A debugger detaching while a pause is being announced must not race the
// announcement: the channel it closes is the one being sent on.
func TestUnsubscribingWhileAPauseIsAnnouncedDoesNotRace(t *testing.T) {
	for range 200 {
		r := NewRegistry(nil)
		events := r.SubscribeBreakpoints("wf")
		msg := message.AcquireMessage()
		msg.SetID("m")
		done := make(chan struct{})
		go func() {
			r.PauseAtEdge(t.Context(), "wf", "e", "n", msg)
			close(done)
		}()
		r.UnsubscribeDebugger("wf", events)
		for len(r.HeldPauses("wf")) == 0 {
			time.Sleep(time.Millisecond)
		}
		r.DebuggerCommand("wf", "e/m", "resume")
		<-done
		r.Close()
		msg.Release()
	}
}
