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
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
	_ "modernc.org/sqlite"

	"github.com/gsoultan/hermod/internal/agenthub"
	"github.com/gsoultan/hermod/internal/storage"
	sqlstore "github.com/gsoultan/hermod/internal/storage/sql"
	pb "github.com/gsoultan/hermod/pkg/agent/proto"
	"github.com/gsoultan/hermod/pkg/comm/message"
	agentsink "github.com/gsoultan/hermod/pkg/comm/sink/agent"
	agentsource "github.com/gsoultan/hermod/pkg/comm/source/agent"
)

// The whole path, with Hermod's own pieces in place of stand-ins: a device on
// the LAN, claimed in Hermod's database, read by this agent, delivered by
// Hermod's agent service to a workflow's Agent source, and deleted from the
// agent's spool only after the workflow acknowledges it.
//
// This test links Hermod packages; the agent's binary still does not (the
// guard test lists what the binary links, not its tests).
func TestAClaimedDeviceReachesAWorkflowThroughHermod(t *testing.T) {
	scale := listen(t)
	defer scale.Close()
	go func() {
		conn, err := scale.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = conn.Write([]byte("ST,GS,+0012.40kg\r\nST,GS,+0012.38kg\r\n"))
		time.Sleep(2 * time.Second)
	}()

	db, err := sql.Open("sqlite", fmt.Sprintf("file:e2e_%s?mode=memory&cache=shared", t.Name()))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	st := sqlstore.NewSQLStorage(db, "sqlite")
	if err := st.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	token, hash, err := storage.NewAgentToken("agt1")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.(storage.AgentStore).CreateAgent(t.Context(), storage.Agent{
		ID: "agt1", VHost: "plant", Name: "line 1", TokenHash: hash,
		Devices: []storage.AgentDevice{{DeviceID: "tcp-scale", Kind: storage.AgentDeviceTCP, Name: "scale", Enabled: true,
			TCP: &storage.AgentTCP{Address: scale.Addr().String(), Mode: "client"}}},
	}); err != nil {
		t.Fatal(err)
	}

	hub := agenthub.New(func() any { return st }, nil)
	ln := bufconn.Listen(1 << 20)
	g := grpc.NewServer()
	pb.RegisterAgentServiceServer(g, hub)
	go func() { _ = g.Serve(ln) }()
	defer g.Stop()

	src, err := agentsource.New(hub, agentsource.Config{AgentID: "agt1", VHost: "plant"})
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()

	opts := options(t.TempDir(), func(ctx context.Context, _ string) (net.Conn, error) { return ln.DialContext(ctx) })
	opts.Token = token
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, opts) }()
	defer func() { cancel(); <-done }()

	readCtx, stop := context.WithTimeout(t.Context(), 10*time.Second)
	defer stop()
	for _, want := range []string{"ST,GS,+0012.40kg", "ST,GS,+0012.38kg"} {
		msg, err := src.Read(readCtx)
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		var body map[string]any
		if err := json.Unmarshal(msg.Payload(), &body); err != nil {
			t.Fatal(err)
		}
		if body["text"] != want || body["device"] != "scale" || msg.Table() != "scale" {
			t.Fatalf("message = %s", msg.Payload())
		}
		if float64(hub.Live("agt1").Acked) >= body["seq"].(float64) {
			t.Fatal("Hermod acknowledged a record before the workflow did")
		}
		if err := src.Ack(readCtx, msg); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, "the agent told both records are done", func() bool { return hub.Live("agt1").Acked == 2 })
	eventually(t, "the agent's report saved", func() bool {
		a, _ := st.(storage.AgentStore).GetAgent(t.Context(), "agt1")
		return a.Status.Version == "test" && len(a.Status.Ports) == 2
	})
}

// A workflow's Agent device sink reaches a device on the LAN through Hermod
// and this agent: the saved template is filled from the message, written to
// the device, and the device's reply comes back.
func TestAWorkflowCommandReachesTheDeviceThroughHermod(t *testing.T) {
	scale := listen(t)
	defer scale.Close()
	got := make(chan string, 1)
	go func() {
		conn, err := scale.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 64)
		n, _ := conn.Read(buf)
		got <- string(buf[:n])
		_, _ = conn.Write([]byte("OK 12.5\r\n"))
		time.Sleep(2 * time.Second)
	}()

	db, err := sql.Open("sqlite", fmt.Sprintf("file:e2e_%s?mode=memory&cache=shared", t.Name()))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	st := sqlstore.NewSQLStorage(db, "sqlite")
	if err := st.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	token, hash, _ := storage.NewAgentToken("agt1")
	if err := st.(storage.AgentStore).CreateAgent(t.Context(), storage.Agent{
		ID: "agt1", VHost: "plant", Name: "line 1", TokenHash: hash,
		Devices: []storage.AgentDevice{{DeviceID: "tcp-scale", Kind: storage.AgentDeviceTCP, Name: "scale", Enabled: true,
			TCP: &storage.AgentTCP{Address: scale.Addr().String(), Mode: "client"}, AllowCommands: true,
			Commands: []storage.AgentCommand{{Name: "set", Payload: `S {{target}}\r\n`, ReplyTimeoutMS: 3000}}}},
	}); err != nil {
		t.Fatal(err)
	}
	hub := agenthub.New(func() any { return st }, nil)
	ln := bufconn.Listen(1 << 20)
	g := grpc.NewServer()
	pb.RegisterAgentServiceServer(g, hub)
	go func() { _ = g.Serve(ln) }()
	defer g.Stop()

	opts := options(t.TempDir(), func(ctx context.Context, _ string) (net.Conn, error) { return ln.DialContext(ctx) })
	opts.Token = token
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, opts) }()
	defer func() { cancel(); <-done }()
	eventually(t, "the agent connected", func() bool { return hub.Live("agt1").Connected })

	sink, err := agentsink.New(hub, agentsink.Config{AgentID: "agt1", DeviceID: "tcp-scale", Command: "set", VHost: "plant"})
	if err != nil {
		t.Fatal(err)
	}
	msg := message.AcquireMessage()
	msg.SetData("target", 12.5)
	// The reader starts and connects to the device a moment after the
	// settings arrive; until then the command is refused, not written.
	var werr error
	eventually(t, "the command written", func() bool {
		werr = sink.Write(t.Context(), msg)
		var ce *agenthub.CommandError
		notYet := errors.As(werr, &ce) && !ce.Written &&
			(strings.Contains(ce.Msg, "not connected") || strings.Contains(ce.Msg, "not claimed"))
		return !notYet
	})
	if werr != nil {
		t.Fatalf("Write: %v", werr)
	}
	if w := <-got; w != "S 12.5\r\n" {
		t.Fatalf("the device got %q", w)
	}
	res, err := hub.RunCommand(t.Context(), "plant", "agt1", "tcp-scale", "nope", nil)
	if err == nil {
		t.Fatalf("an unsaved command ran: %+v", res)
	}
}
