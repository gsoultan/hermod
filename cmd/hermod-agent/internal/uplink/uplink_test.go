// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package uplink

import (
	"context"
	"errors"
	"net"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod/cmd/hermod-agent/internal/spool"
	pb "github.com/gsoultan/hermod/pkg/agent/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
)

// fakeServer plays the Hermod side of the stream.
type fakeServer struct {
	pb.UnimplementedAgentServiceServer

	mu        sync.Mutex
	acked     uint64 // what Welcome reports
	autoAck   bool
	received  []uint64
	hellos    []*pb.Hello
	devices   [][]*pb.Device
	tokens    []string
	settings  *pb.DeviceSettingsList
	beats     int
	dropAfter int // close the stream after this many records, once
	streams   int
	changed   chan struct{}
	onBatch   func(records int)
	commands  []*pb.Command // sent after the welcome
	results   []*pb.CommandResult
}

func newFake() *fakeServer { return &fakeServer{autoAck: true, changed: make(chan struct{}, 1000)} }

func (f *fakeServer) poke() {
	select {
	case f.changed <- struct{}{}:
	default:
	}
}

func (f *fakeServer) Connect(stream grpc.BidiStreamingServer[pb.ConnectRequest, pb.ConnectResponse]) error {
	md, _ := metadata.FromIncomingContext(stream.Context())
	f.mu.Lock()
	f.streams++
	f.tokens = append(f.tokens, md.Get("authorization")...)
	f.mu.Unlock()

	first, err := stream.Recv()
	if err != nil {
		return err
	}
	f.mu.Lock()
	f.hellos = append(f.hellos, first.GetHello())
	welcome := &pb.Welcome{AgentId: "agent-1", AckedSeq: f.acked}
	settings := f.settings
	commands := f.commands
	f.mu.Unlock()
	if err := stream.Send(&pb.ConnectResponse{Body: &pb.ConnectResponse_Welcome{Welcome: welcome}}); err != nil {
		return err
	}
	if settings != nil {
		_ = stream.Send(&pb.ConnectResponse{Body: &pb.ConnectResponse_Settings{Settings: settings}})
	}
	for _, c := range commands {
		_ = stream.Send(&pb.ConnectResponse{Body: &pb.ConnectResponse_Command{Command: c}})
	}
	for {
		m, err := stream.Recv()
		if err != nil {
			return err
		}
		if err := f.handle(stream, m); err != nil {
			return err
		}
		f.poke()
	}
}

func (f *fakeServer) handle(stream grpc.BidiStreamingServer[pb.ConnectRequest, pb.ConnectResponse], m *pb.ConnectRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch b := m.GetBody().(type) {
	case *pb.ConnectRequest_Devices:
		f.devices = append(f.devices, b.Devices.GetDevices())
	case *pb.ConnectRequest_Heartbeat:
		f.beats++
	case *pb.ConnectRequest_CommandResult:
		f.results = append(f.results, b.CommandResult)
	case *pb.ConnectRequest_Records:
		if f.onBatch != nil {
			f.onBatch(len(b.Records.GetRecords()))
		}
		for _, r := range b.Records.GetRecords() {
			f.received = append(f.received, r.GetSeq())
		}
		if f.dropAfter > 0 && len(f.received) >= f.dropAfter {
			f.dropAfter = 0
			return errors.New("server went away")
		}
		recs := b.Records.GetRecords()
		if f.autoAck && len(recs) > 0 {
			up := recs[len(recs)-1].GetSeq()
			f.acked = up
			return stream.Send(&pb.ConnectResponse{Body: &pb.ConnectResponse_Ack{Ack: &pb.Ack{UpToSeq: up}}})
		}
	}
	return nil
}

func (f *fakeServer) waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		f.mu.Lock()
		ok := cond()
		f.mu.Unlock()
		if ok {
			return
		}
		select {
		case <-f.changed:
		case <-time.After(10 * time.Millisecond):
		case <-deadline:
			t.Fatal("condition not met in time")
		}
	}
}

func startFake(t *testing.T, f *fakeServer) func(context.Context, string) (net.Conn, error) {
	t.Helper()
	ln := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	pb.RegisterAgentServiceServer(srv, f)
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(srv.Stop)
	return func(ctx context.Context, _ string) (net.Conn, error) { return ln.DialContext(ctx) }
}

// runUntilTestEnds runs the agent's stream for the rest of the test and waits
// for it to return before the spool is closed and its directory removed.
// Left running, it could still write an acknowledgement into the spool after
// the test ended, and the temporary directory's cleanup failed with
// "directory not empty".
func runUntilTestEnds(t *testing.T, cfg Config) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); _ = Run(ctx, cfg) }()
	t.Cleanup(func() { cancel(); <-done })
}

func openSpool(t *testing.T) *spool.Spool {
	t.Helper()
	s, err := spool.Open(t.TempDir(), spool.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func appendN(t *testing.T, s *spool.Spool, n int) {
	t.Helper()
	for range n {
		if _, err := s.Append([]spool.Record{{DeviceID: "d", Raw: []byte("x")}}); err != nil {
			t.Fatal(err)
		}
	}
}

func baseConfig(dial func(context.Context, string) (net.Conn, error), s *spool.Spool) Config {
	return Config{
		Address:    "passthrough:///hermod",
		Token:      "secret-token",
		Creds:      insecure.NewCredentials(),
		Dialer:     dial,
		Spool:      s,
		Hello:      func() *pb.Hello { return &pb.Hello{Version: "test"} },
		Devices:    func() []*pb.Device { return []*pb.Device{{DeviceId: "port:/dev/ttyS0"}} },
		MinBackoff: time.Millisecond,
		MaxBackoff: 10 * time.Millisecond,
	}
}

func TestDeliversRecordsAndDeletesThemOnAck(t *testing.T) {
	f := newFake()
	s := openSpool(t)
	appendN(t, s, 3)
	runUntilTestEnds(t, baseConfig(startFake(t, f), s))

	f.waitFor(t, func() bool { return len(f.received) == 3 })
	appendN(t, s, 2)
	f.waitFor(t, func() bool { return len(f.received) == 5 })
	deadline := time.Now().Add(5 * time.Second)
	for s.Stats().Waiting != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if w := s.Stats().Waiting; w != 0 {
		t.Fatalf("waiting after ack = %d", w)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.tokens) == 0 || f.tokens[0] != "Bearer secret-token" {
		t.Fatalf("authorization = %q", f.tokens)
	}
	if len(f.devices) == 0 || f.devices[0][0].GetDeviceId() != "port:/dev/ttyS0" {
		t.Fatalf("devices = %v", f.devices)
	}
	if f.hellos[0].GetVersion() != "test" || f.hellos[0].GetLastSeq() != 3 {
		t.Fatalf("hello = %v", f.hellos[0])
	}
}

// When the stream breaks before the server acknowledges, nothing is lost: the
// agent reconnects and sends again from the last acknowledged record.
func TestResendsUnacknowledgedRecordsAfterReconnect(t *testing.T) {
	f := newFake()
	f.autoAck = false
	f.dropAfter = 4
	s := openSpool(t)
	appendN(t, s, 4)
	runUntilTestEnds(t, baseConfig(startFake(t, f), s))

	f.waitFor(t, func() bool { return f.streams >= 2 && len(f.received) >= 8 })
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.received[4] != 1 || f.received[7] != 4 {
		t.Fatalf("received = %v, want 1..4 twice", f.received)
	}
}

// The server may already hold records the agent never saw acknowledged, for
// example when the ack was lost with the connection. Welcome says so, and the
// agent skips them rather than sending duplicates.
func TestWelcomeAcknowledgesWhatTheServerAlreadyHas(t *testing.T) {
	f := newFake()
	f.acked = 2
	s := openSpool(t)
	appendN(t, s, 3)
	runUntilTestEnds(t, baseConfig(startFake(t, f), s))

	f.waitFor(t, func() bool { return len(f.received) >= 1 })
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.received[0] != 3 {
		t.Fatalf("first record sent = %d, want 3", f.received[0])
	}
}

func TestSettingsReachTheAgent(t *testing.T) {
	f := newFake()
	f.settings = &pb.DeviceSettingsList{Devices: []*pb.DeviceSettings{{DeviceId: "usb:1:2:3", Enabled: true}}}
	s := openSpool(t)
	got := make(chan *pb.DeviceSettingsList, 1)
	cfg := baseConfig(startFake(t, f), s)
	cfg.OnSettings = func(l *pb.DeviceSettingsList) { got <- l }
	runUntilTestEnds(t, cfg)
	select {
	case l := <-got:
		if l.GetDevices()[0].GetDeviceId() != "usb:1:2:3" {
			t.Fatalf("settings = %v", l)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no settings")
	}
}

// Without acknowledgements the agent stops after Window records instead of
// streaming the whole spool at a server that is not keeping up.
func TestStopsAtTheWindowUntilAcknowledged(t *testing.T) {
	f := newFake()
	f.autoAck = false
	s := openSpool(t)
	appendN(t, s, 10)
	cfg := baseConfig(startFake(t, f), s)
	cfg.Window, cfg.BatchSize = 4, 2
	runUntilTestEnds(t, cfg)
	f.waitFor(t, func() bool { return len(f.received) >= 4 })
	time.Sleep(50 * time.Millisecond)
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.received) != 4 {
		t.Fatalf("sent %d records with a window of 4", len(f.received))
	}
}

func TestDeviceChangesAreSent(t *testing.T) {
	f := newFake()
	s := openSpool(t)
	cfg := baseConfig(startFake(t, f), s)
	var mu sync.Mutex
	list := []*pb.Device{{DeviceId: "a"}}
	cfg.Devices = func() []*pb.Device {
		mu.Lock()
		defer mu.Unlock()
		return list
	}
	changed := make(chan struct{}, 1)
	cfg.DevicesChanged = changed
	runUntilTestEnds(t, cfg)
	f.waitFor(t, func() bool { return len(f.devices) == 1 })
	mu.Lock()
	list = []*pb.Device{{DeviceId: "a"}, {DeviceId: "b"}}
	mu.Unlock()
	changed <- struct{}{}
	f.waitFor(t, func() bool { return len(f.devices) == 2 && len(f.devices[1]) == 2 })
}

func TestTransportCredentialsRefuseInsecureOffLoopback(t *testing.T) {
	if _, err := TransportCredentials("hermod.example.com:9443", "", true); err == nil {
		t.Fatal("plaintext to a remote host was allowed")
	}
	for _, addr := range []string{"localhost:9443", "127.0.0.1:9443", "[::1]:9443"} {
		if _, err := TransportCredentials(addr, "", true); err != nil {
			t.Errorf("%s: %v", addr, err)
		}
	}
	c, err := TransportCredentials("hermod.example.com:9443", "", false)
	if err != nil || c.Info().SecurityProtocol != "tls" {
		t.Fatalf("TLS credentials = %v, %v", c, err)
	}
	if _, err := TransportCredentials("hermod.example.com:9443", "/no/such/ca.pem", false); err == nil {
		t.Fatal("missing CA file accepted")
	}
}

// Hermod refuses a gRPC message over 4 MiB and the stream with it, so a batch
// of 256 large records would never be delivered: the agent would reconnect and
// resend the same batch for ever. A batch stops at MaxBatchBytes of payload,
// and a single record larger than that still goes, alone.
func TestBatchesStopAtTheByteLimit(t *testing.T) {
	f := newFake()
	s := openSpool(t)
	big := make([]byte, 600)
	for range 5 {
		if _, err := s.Append([]spool.Record{{DeviceID: "d", Raw: big}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Append([]spool.Record{{DeviceID: "d", Raw: make([]byte, 2500)}}); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var sizes []int
	f.onBatch = func(n int) { mu.Lock(); sizes = append(sizes, n); mu.Unlock() }
	cfg := baseConfig(startFake(t, f), s)
	cfg.MaxBatchBytes = 1300
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { _ = Run(ctx, cfg); close(done) }()
	// Run acks into the spool's directory; it has to stop before the test's
	// temporary directory is removed.
	defer func() { cancel(); <-done }()

	f.waitFor(t, func() bool { return len(f.received) == 6 })
	mu.Lock()
	defer mu.Unlock()
	if want := []int{2, 2, 1, 1}; !reflect.DeepEqual(sizes, want) {
		t.Fatalf("batch sizes = %v, want %v", sizes, want)
	}
}

func TestCommandsAreAnsweredOnTheStream(t *testing.T) {
	f := newFake()
	f.commands = []*pb.Command{
		{CommandId: "c1", DeviceId: "dev", Payload: []byte("T")},
		{CommandId: "c2", DeviceId: "dev", Payload: []byte("W")},
	}
	s := openSpool(t)
	cfg := baseConfig(startFake(t, f), s)
	cfg.OnCommand = func(_ context.Context, c *pb.Command) *pb.CommandResult {
		return &pb.CommandResult{CommandId: c.GetCommandId(), Status: pb.CommandStatus_COMMAND_STATUS_REPLIED, Reply: append([]byte("re:"), c.GetPayload()...)}
	}
	runUntilTestEnds(t, cfg)
	f.waitFor(t, func() bool { return len(f.results) == 2 })
	f.mu.Lock()
	defer f.mu.Unlock()
	got := map[string]string{}
	for _, r := range f.results {
		got[r.GetCommandId()] = string(r.GetReply())
	}
	if got["c1"] != "re:T" || got["c2"] != "re:W" {
		t.Fatalf("results = %v", got)
	}
}

// An agent built without command support says so rather than leaving the
// server waiting for a result that will never come.
func TestCommandsFailWithoutAHandler(t *testing.T) {
	f := newFake()
	f.commands = []*pb.Command{{CommandId: "c1", DeviceId: "dev", Payload: []byte("T")}}
	runUntilTestEnds(t, baseConfig(startFake(t, f), openSpool(t)))
	f.waitFor(t, func() bool { return len(f.results) == 1 })
	f.mu.Lock()
	defer f.mu.Unlock()
	if r := f.results[0]; r.GetStatus() != pb.CommandStatus_COMMAND_STATUS_FAILED || r.GetWritten() {
		t.Fatalf("result = %v", r)
	}
}
