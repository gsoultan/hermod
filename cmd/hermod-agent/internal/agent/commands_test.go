// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package agent

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"

	pb "github.com/gsoultan/hermod/pkg/agent/proto"
)

// scaleThatAnswers is a LAN scale that answers "W" with a weight.
func scaleThatAnswers(t *testing.T) net.Listener {
	t.Helper()
	ln := listen(t)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				r := bufio.NewReader(conn)
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					if strings.TrimSpace(line) == "W" {
						_, _ = conn.Write([]byte("ST,GS,+0001.00kg\r\n"))
					}
				}
			}()
		}
	}()
	return ln
}

// runWithCommands starts an agent with one TCP device and returns a function
// that sends it a command and waits for the result.
func runWithCommands(t *testing.T, allow bool) func(*pb.Command) *pb.CommandResult {
	t.Helper()
	scale := scaleThatAnswers(t)
	srv := &server{
		commands: make(chan *pb.Command),
		settings: &pb.DeviceSettingsList{Devices: []*pb.DeviceSettings{{
			DeviceId: "tcp-scale", Kind: pb.DeviceKind_DEVICE_KIND_TCP, Enabled: true, AllowCommands: allow,
			Tcp: &pb.TcpSettings{Address: scale.Addr().String()},
		}}},
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, options(t.TempDir(), serve(t, srv))) }()
	t.Cleanup(func() { cancel(); <-done })
	eventually(t, "the scale connected", func() bool {
		_, devs := srv.snapshot()
		for _, d := range devs {
			if d.GetDeviceId() == "tcp-scale" && d.GetPresent() {
				return true
			}
		}
		return false
	})
	return func(c *pb.Command) *pb.CommandResult {
		srv.mu.Lock()
		before := len(srv.results)
		srv.mu.Unlock()
		srv.commands <- c
		var r *pb.CommandResult
		eventually(t, "the command result", func() bool {
			srv.mu.Lock()
			defer srv.mu.Unlock()
			if len(srv.results) > before {
				r = srv.results[before]
				return true
			}
			return false
		})
		return r
	}
}

func TestACommandReachesTheDeviceAndItsReplyComesBack(t *testing.T) {
	send := runWithCommands(t, true)
	r := send(&pb.Command{CommandId: "c1", DeviceId: "tcp-scale", Payload: []byte("W\r\n"), ReplyTimeoutMs: 2000})
	if r.GetStatus() != pb.CommandStatus_COMMAND_STATUS_REPLIED || string(r.GetReply()) != "ST,GS,+0001.00kg" || !r.GetWritten() {
		t.Fatalf("result = %v", r)
	}
	r = send(&pb.Command{CommandId: "c2", DeviceId: "tcp-scale", Payload: []byte("T\r\n")})
	if r.GetCommandId() != "c2" || r.GetStatus() != pb.CommandStatus_COMMAND_STATUS_WRITTEN || !r.GetWritten() {
		t.Fatalf("result = %v", r)
	}
}

// The agent decides for itself: a device without Allow commands is never
// written to, whatever the server sends.
func TestACommandForADeviceWithoutAllowCommandsIsRefused(t *testing.T) {
	send := runWithCommands(t, false)
	r := send(&pb.Command{CommandId: "c1", DeviceId: "tcp-scale", Payload: []byte("T\r\n")})
	if r.GetStatus() != pb.CommandStatus_COMMAND_STATUS_FAILED || r.GetWritten() || !strings.Contains(r.GetError(), "not allowed") {
		t.Fatalf("result = %v", r)
	}
}

func TestACommandForAnUnknownDeviceIsRefused(t *testing.T) {
	send := runWithCommands(t, true)
	r := send(&pb.Command{CommandId: "c1", DeviceId: "nope", Payload: []byte("T")})
	if r.GetStatus() != pb.CommandStatus_COMMAND_STATUS_FAILED || r.GetWritten() {
		t.Fatalf("result = %v", r)
	}
}

func TestOversizeAndEmptyCommandsAreRefused(t *testing.T) {
	send := runWithCommands(t, true)
	for _, p := range [][]byte{nil, make([]byte, maxCommandBytes+1)} {
		r := send(&pb.Command{CommandId: "c", DeviceId: "tcp-scale", Payload: p})
		if r.GetStatus() != pb.CommandStatus_COMMAND_STATUS_FAILED || r.GetWritten() {
			t.Fatalf("%d bytes: result = %v", len(p), r)
		}
	}
}

func TestNoReplyInTimeFailsAfterTheWrite(t *testing.T) {
	send := runWithCommands(t, true)
	r := send(&pb.Command{CommandId: "c1", DeviceId: "tcp-scale", Payload: []byte("X\r\n"), ReplyTimeoutMs: 50})
	if r.GetStatus() != pb.CommandStatus_COMMAND_STATUS_FAILED || !r.GetWritten() || !strings.Contains(r.GetError(), "no reply") {
		t.Fatalf("result = %v", r)
	}
}
