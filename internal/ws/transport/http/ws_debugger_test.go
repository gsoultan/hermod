// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/engine/registry"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

func dialDebugger(t *testing.T, reg *registry.Registry, query string) *websocket.Conn {
	t.Helper()
	h := &WSHandler{Handler: &handlers.Handler{Registry: reg}}
	srv := httptest.NewServer(http.HandlerFunc(h.HandleDebuggerWS))
	t.Cleanup(srv.Close)
	conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/api/ws/debugger?"+query, nil)
	if err != nil {
		t.Fatalf("dial debugger: %v", err)
	}
	_ = resp.Body.Close()
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// The editor attaches in breakpoints mode: messages stop at edge breakpoints,
// not before every node, and a pause is answered by its pause_id.
func TestDebuggerWSBreakpointsModeAnswersAnEdgePause(t *testing.T) {
	reg := registry.NewRegistry(nil)
	defer reg.Close()
	conn := dialDebugger(t, reg, "workflow_id=wf-1&mode=breakpoints")

	waitFor(t, "the breakpoint watcher", func() bool { return reg.BreakpointsWatched("wf-1") })
	if reg.IsDebuggerAttached("wf-1") {
		t.Fatal("breakpoints mode turned on stepping through every node")
	}

	msg := message.AcquireMessage()
	msg.SetID("m-1")
	msg.SetData("n", 1)
	released := make(chan struct{})
	go func() {
		reg.PauseAtEdge(context.Background(), "wf-1", "e-1", "n-2", msg)
		close(released)
	}()

	var ev registry.DebuggerEvent
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if err := conn.ReadJSON(&ev); err != nil {
		t.Fatalf("read pause: %v", err)
	}
	if ev.State != "paused" || ev.EdgeID != "e-1" || ev.PauseID == "" {
		t.Fatalf("event = %+v, want a pause on e-1 with a pause_id", ev)
	}

	if err := conn.WriteJSON(map[string]string{"action": "resume", "msg_id": ev.MsgID, "pause_id": ev.PauseID}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-released:
	case <-time.After(5 * time.Second):
		t.Fatal("the edge pause was not released by its pause_id")
	}
}

// With no mode the debugger steps, as the Debugger tab always has.
func TestDebuggerWSDefaultModeSteps(t *testing.T) {
	reg := registry.NewRegistry(nil)
	defer reg.Close()
	dialDebugger(t, reg, "workflow_id=wf-2")
	waitFor(t, "the stepping debugger", func() bool { return reg.IsDebuggerAttached("wf-2") })
}

// An editor that opens, or reconnects, while a message is already held is told
// about it first, so it can still resume it.
func TestDebuggerWSSendsPausesAlreadyHeld(t *testing.T) {
	reg := registry.NewRegistry(nil)
	defer reg.Close()

	msg := message.AcquireMessage()
	msg.SetID("m-held")
	go reg.PauseAtEdge(t.Context(), "wf-3", "e-1", "n-2", msg)
	waitFor(t, "the pause", func() bool { return len(reg.HeldPauses("wf-3")) == 1 })

	conn := dialDebugger(t, reg, "workflow_id=wf-3&mode=breakpoints")
	var ev registry.DebuggerEvent
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if err := conn.ReadJSON(&ev); err != nil {
		t.Fatalf("read: %v", err)
	}
	if ev.State != "paused" || ev.PauseID != "e-1/m-held" {
		t.Errorf("first event = %+v, want the pause already held", ev)
	}
}
