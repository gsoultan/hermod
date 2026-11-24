// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

// Package uplink is hermod-agent's one connection to a Hermod server. It
// sends records from the spool in order, deletes them only when the server
// acknowledges them, and after any failure reconnects and sends again from
// the last acknowledged record.
package uplink

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/gsoultan/hermod/cmd/hermod-agent/internal/spool"
	pb "github.com/gsoultan/hermod/pkg/agent/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
)

// Config is how to reach the server and what to tell it.
type Config struct {
	// Address is the server's gRPC address, host:port.
	Address string
	// Token is the agent's own token, sent as a bearer token.
	Token string
	Creds credentials.TransportCredentials
	// Dialer replaces the network dialer; tests use it.
	Dialer func(ctx context.Context, addr string) (net.Conn, error)

	Spool *spool.Spool
	// Hello describes the agent; the spool's sequence numbers are filled in.
	Hello func() *pb.Hello
	// Devices returns the devices to report, and DevicesChanged signals when
	// that list has changed.
	Devices        func() []*pb.Device
	DevicesChanged <-chan struct{}
	// OnSettings receives the claimed devices' settings.
	OnSettings func(*pb.DeviceSettingsList)
	// OnCommand carries out a command from the server and returns its
	// result. It runs on its own goroutine, at most maxCommands at once, and
	// must return when ctx ends. Without it every command fails.
	OnCommand func(ctx context.Context, c *pb.Command) *pb.CommandResult
	// Heartbeat adds to the spool figures the uplink fills in itself; it may
	// be nil.
	Heartbeat      func(*pb.Heartbeat)
	HeartbeatEvery time.Duration
	// OnState reports connection changes, for logs.
	OnState func(connected bool, err error)

	// BatchSize is the most records per message; default 256. Window is the
	// most records sent but not yet acknowledged; default 4096.
	BatchSize int
	Window    uint64
	// MaxBatchBytes is the most payload bytes per message; default 1 MiB.
	// Hermod refuses a message over 4 MiB, and the stream with it. A record
	// larger than this still goes, alone.
	MaxBatchBytes int

	MinBackoff, MaxBackoff time.Duration
}

func (c *Config) defaults() {
	if c.BatchSize <= 0 {
		c.BatchSize = 256
	}
	if c.Window == 0 {
		c.Window = 4096
	}
	if c.MaxBatchBytes <= 0 {
		c.MaxBatchBytes = 1 << 20
	}
	if c.HeartbeatEvery <= 0 {
		c.HeartbeatEvery = 15 * time.Second
	}
	if c.MinBackoff <= 0 {
		c.MinBackoff = time.Second
	}
	if c.MaxBackoff <= 0 {
		c.MaxBackoff = 30 * time.Second
	}
}

// maxCommands is how many commands may run at once. A command beyond it
// fails at once rather than queueing, so it never reaches a device late.
const maxCommands = 16

// Run keeps the connection up until ctx ends.
func Run(ctx context.Context, cfg Config) error {
	cfg.defaults()
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(cfg.Creds),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{Time: 30 * time.Second, Timeout: 10 * time.Second}),
	}
	if cfg.Dialer != nil {
		opts = append(opts, grpc.WithContextDialer(cfg.Dialer))
	}
	conn, err := grpc.NewClient(cfg.Address, opts...)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	client := pb.NewAgentServiceClient(conn)

	wait := cfg.MinBackoff
	for ctx.Err() == nil {
		sent, err := session(ctx, client, &cfg)
		if ctx.Err() != nil {
			return nil
		}
		if cfg.OnState != nil {
			cfg.OnState(false, err)
		}
		if sent {
			wait = cfg.MinBackoff
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(wait):
		}
		wait = min(wait*2, cfg.MaxBackoff)
	}
	return nil
}

// stream is the client side of Connect.
type stream = grpc.BidiStreamingClient[pb.ConnectRequest, pb.ConnectResponse]

// session runs one stream until it fails. It reports whether the server
// welcomed the agent, so a working connection resets the backoff.
func session(ctx context.Context, client pb.AgentServiceClient, cfg *Config) (bool, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+cfg.Token)
	st, err := client.Connect(ctx)
	if err != nil {
		return false, err
	}
	if err := handshake(st, cfg); err != nil {
		return false, err
	}
	if cfg.OnState != nil {
		cfg.OnState(true, nil)
	}
	acks := make(chan struct{}, 1)
	errc := make(chan error, 1)
	received := make(chan struct{})
	cmds := newCommands(ctx, cfg.OnCommand)
	go func() {
		defer close(received)
		errc <- receive(st, cfg, acks, cmds)
	}()
	err = send(ctx, st, cfg, acks, errc, cmds.results)
	// The receiver acknowledges into the spool. It has to be finished before
	// the session returns, or it could write to a spool its owner has
	// already closed. Commands still running are cancelled; the server has
	// already failed them with the stream.
	cancel()
	<-received
	cmds.wait()
	return true, err
}

func handshake(st stream, cfg *Config) error {
	hello := &pb.Hello{}
	if cfg.Hello != nil {
		hello = cfg.Hello()
	}
	stats := cfg.Spool.Stats()
	hello.LastSeq, hello.AckedSeq = stats.LastSeq, stats.AckedSeq
	if err := st.Send(&pb.ConnectRequest{Body: &pb.ConnectRequest_Hello{Hello: hello}}); err != nil {
		return err
	}
	first, err := st.Recv()
	if err != nil {
		return err
	}
	welcome := first.GetWelcome()
	if welcome == nil {
		return errors.New("server did not answer with a welcome")
	}
	// The server may already hold records whose ack was lost with the last
	// connection. It cannot hold more than the agent ever wrote.
	if up := min(welcome.GetAckedSeq(), stats.LastSeq); up > stats.AckedSeq {
		if err := cfg.Spool.Ack(up); err != nil {
			return err
		}
	}
	return sendDevices(st, cfg)
}

func sendDevices(st stream, cfg *Config) error {
	if cfg.Devices == nil {
		return nil
	}
	return st.Send(&pb.ConnectRequest{Body: &pb.ConnectRequest_Devices{Devices: &pb.DeviceList{Devices: cfg.Devices()}}})
}

// receive handles what the server sends until the stream ends.
func receive(st stream, cfg *Config, acks chan<- struct{}, cmds *commands) error {
	for {
		m, err := st.Recv()
		if err != nil {
			return err
		}
		switch b := m.GetBody().(type) {
		case *pb.ConnectResponse_Ack:
			if err := cfg.Spool.Ack(b.Ack.GetUpToSeq()); err != nil {
				return fmt.Errorf("server acknowledged a record the agent never sent: %w", err)
			}
			select {
			case acks <- struct{}{}:
			default:
			}
		case *pb.ConnectResponse_Settings:
			if cfg.OnSettings != nil {
				cfg.OnSettings(b.Settings)
			}
		case *pb.ConnectResponse_Command:
			cmds.start(b.Command)
		}
	}
}

// send streams records in order, never more than Window ahead of the last
// acknowledgement, and reports devices and heartbeats as they come due.
func send(ctx context.Context, st stream, cfg *Config, acks <-chan struct{}, errc <-chan error, results <-chan *pb.CommandResult) error {
	cursor := cfg.Spool.Stats().AckedSeq
	beat := time.NewTicker(cfg.HeartbeatEvery)
	defer beat.Stop()
	for {
		stats := cfg.Spool.Stats()
		cursor = max(cursor, stats.AckedSeq)
		if cursor-stats.AckedSeq < cfg.Window {
			n := min(cfg.BatchSize, int(cfg.Window-(cursor-stats.AckedSeq)))
			next, err := sendBatch(st, cfg.Spool, cursor, n, cfg.MaxBatchBytes)
			if err != nil {
				return err
			}
			if next != cursor {
				cursor = next
				continue
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errc:
			return err
		case <-cfg.Spool.Notify():
		case <-acks:
		case <-cfg.DevicesChanged:
			if err := sendDevices(st, cfg); err != nil {
				return err
			}
		case r := <-results:
			if err := st.Send(&pb.ConnectRequest{Body: &pb.ConnectRequest_CommandResult{CommandResult: r}}); err != nil {
				return err
			}
		case <-beat.C:
			if err := st.Send(&pb.ConnectRequest{Body: &pb.ConnectRequest_Heartbeat{Heartbeat: heartbeat(cfg)}}); err != nil {
				return err
			}
		}
	}
}

// sendBatch sends up to n records after cursor, and no more than maxBytes of
// payload unless one record alone is larger, and returns the new cursor.
func sendBatch(st stream, sp *spool.Spool, cursor uint64, n, maxBytes int) (uint64, error) {
	recs, err := sp.ReadFrom(cursor, n)
	if err != nil || len(recs) == 0 {
		return cursor, err
	}
	size := len(recs[0].Raw)
	for i := 1; i < len(recs); i++ {
		if size += len(recs[i].Raw); size > maxBytes {
			recs = recs[:i]
			break
		}
	}
	batch := &pb.RecordBatch{Records: make([]*pb.Record, len(recs))}
	for i, r := range recs {
		batch.Records[i] = &pb.Record{
			Seq:                r.Seq,
			DeviceId:           r.DeviceID,
			ReceivedAtUnixNano: r.ReceivedAt,
			Raw:                r.Raw,
			Port:               r.Port,
		}
	}
	if err := st.Send(&pb.ConnectRequest{Body: &pb.ConnectRequest_Records{Records: batch}}); err != nil {
		return cursor, err
	}
	return recs[len(recs)-1].Seq, nil
}

func heartbeat(cfg *Config) *pb.Heartbeat {
	s := cfg.Spool.Stats()
	hb := &pb.Heartbeat{
		ClockUnixMs:  time.Now().UnixMilli(),
		SpoolWaiting: s.Waiting,
		SpoolBytes:   uint64(max(s.Bytes, 0)),
		SpoolDropped: s.Dropped,
	}
	if s.OldestUnixNano > 0 {
		hb.SpoolOldestUnixMs = s.OldestUnixNano / int64(time.Millisecond)
	}
	if cfg.Heartbeat != nil {
		cfg.Heartbeat(hb)
	}
	return hb
}

// commands runs the server's commands for one session.
type commands struct {
	ctx     context.Context
	handle  func(context.Context, *pb.Command) *pb.CommandResult
	slots   chan struct{}
	results chan *pb.CommandResult
	wg      sync.WaitGroup
}

func newCommands(ctx context.Context, handle func(context.Context, *pb.Command) *pb.CommandResult) *commands {
	return &commands{
		ctx:     ctx,
		handle:  handle,
		slots:   make(chan struct{}, maxCommands),
		results: make(chan *pb.CommandResult, maxCommands),
	}
}

func failed(c *pb.Command, msg string) *pb.CommandResult {
	return &pb.CommandResult{CommandId: c.GetCommandId(), Status: pb.CommandStatus_COMMAND_STATUS_FAILED, Error: msg}
}

func (c *commands) start(cmd *pb.Command) {
	if c.handle == nil {
		c.reply(failed(cmd, "this agent does not accept commands"))
		return
	}
	select {
	case c.slots <- struct{}{}:
	default:
		c.reply(failed(cmd, "agent busy: too many commands at once"))
		return
	}
	c.wg.Go(func() {
		defer func() { <-c.slots }()
		r := c.handle(c.ctx, cmd)
		if r == nil {
			r = failed(cmd, "no result")
		}
		r.CommandId = cmd.GetCommandId()
		c.reply(r)
	})
}

func (c *commands) reply(r *pb.CommandResult) {
	select {
	case c.results <- r:
	case <-c.ctx.Done():
	}
}

func (c *commands) wait() { c.wg.Wait() }
