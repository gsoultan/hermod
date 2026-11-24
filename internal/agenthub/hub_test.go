// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package agenthub

import (
	"context"
	"errors"
	"net"
	"sort"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/gsoultan/hermod/internal/storage"
	pb "github.com/gsoultan/hermod/pkg/agent/proto"
)

// memStore is an in-memory storage.AgentStore.
type memStore struct {
	mu     sync.Mutex
	agents map[string]storage.Agent
	writes int
}

func newMemStore() *memStore { return &memStore{agents: map[string]storage.Agent{}} }

func (m *memStore) CreateAgent(_ context.Context, a storage.Agent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.agents[a.ID] = a
	return nil
}

func (m *memStore) GetAgent(_ context.Context, id string) (storage.Agent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.agents[id]
	if !ok {
		return storage.Agent{}, storage.ErrNotFound
	}
	return a, nil
}

func (m *memStore) ListAgents(_ context.Context, vhost string) ([]storage.Agent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []storage.Agent
	for _, a := range m.agents {
		if vhost == "" || a.VHost == vhost {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (m *memStore) update(id string, f func(*storage.Agent)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.agents[id]
	if !ok {
		return storage.ErrNotFound
	}
	f(&a)
	m.agents[id] = a
	m.writes++
	return nil
}

func (m *memStore) UpdateAgentDevices(_ context.Context, id, name string, d []storage.AgentDevice) error {
	if err := storage.ValidateAgentDevices(d); err != nil {
		return err
	}
	return m.update(id, func(a *storage.Agent) { a.Name, a.Devices = name, d })
}

func (m *memStore) UpdateAgentStatus(_ context.Context, id string, s storage.AgentStatus) error {
	return m.update(id, func(a *storage.Agent) { a.Status = s })
}

func (m *memStore) UpdateAgentToken(_ context.Context, id, h string) error {
	return m.update(id, func(a *storage.Agent) { a.TokenHash = h })
}

func (m *memStore) DeleteAgent(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.agents[id]; !ok {
		return storage.ErrNotFound
	}
	delete(m.agents, id)
	return nil
}

func (m *memStore) DeleteAgents(context.Context, string) error { return nil }

func (m *memStore) agent(id string) storage.Agent {
	a, _ := m.GetAgent(context.Background(), id)
	return a
}

// fixture is a hub served over an in-process connection, with one agent.
type fixture struct {
	t     *testing.T
	hub   *Hub
	store *memStore
	token string
	conn  *grpc.ClientConn
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	store := newMemStore()
	token, hash, err := storage.NewAgentToken("agt1")
	if err != nil {
		t.Fatal(err)
	}
	_ = store.CreateAgent(t.Context(), storage.Agent{
		ID: "agt1", VHost: "plant", Name: "line 1", TokenHash: hash,
		Devices: []storage.AgentDevice{
			{DeviceID: "dev-a", Kind: storage.AgentDeviceSerial, Name: "scale", Enabled: true,
				Serial: &storage.AgentSerial{BaudRate: 9600}},
			{DeviceID: "dev-b", Kind: storage.AgentDeviceTCP, Name: "plc", Enabled: true,
				TCP: &storage.AgentTCP{Address: "10.0.0.5:502", Mode: "client"}},
		},
	})
	hub := New(func() any { return store }, nil)

	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	pb.RegisterAgentServiceServer(srv, hub)
	go func() { _ = srv.Serve(lis) }()
	conn, err := grpc.NewClient("passthrough:///hub",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(); srv.Stop() })
	return &fixture{t: t, hub: hub, store: store, token: token, conn: conn}
}

// agent is the far end of one stream, as hermod-agent would hold it.
type agent struct {
	t      *testing.T
	stream grpc.BidiStreamingClient[pb.ConnectRequest, pb.ConnectResponse]
	cancel context.CancelFunc

	mu       sync.Mutex
	acked    uint64
	settings []*pb.DeviceSettingsList
	welcome  *pb.Welcome
	err      error
	changed  chan struct{}
	commands []*pb.Command
	updates  []string
	// answer, when set, answers each command the hub sends.
	answer func(*pb.Command) *pb.CommandResult
}

func (f *fixture) connectWith(token string, hello *pb.Hello) (*agent, error) {
	return f.connectAnswering(token, hello, nil)
}

func (f *fixture) connectAnswering(token string, hello *pb.Hello, answer func(*pb.Command) *pb.CommandResult) (*agent, error) {
	ctx, cancel := context.WithCancel(f.t.Context())
	ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
	st, err := pb.NewAgentServiceClient(f.conn).Connect(ctx)
	if err != nil {
		cancel()
		return nil, err
	}
	a := &agent{t: f.t, stream: st, cancel: cancel, changed: make(chan struct{}, 1000), answer: answer}
	f.t.Cleanup(cancel)
	// A Send to a stream the server already ended says only io.EOF; the
	// status comes from Recv.
	_ = st.Send(&pb.ConnectRequest{Body: &pb.ConnectRequest_Hello{Hello: hello}})
	first, err := st.Recv()
	if err != nil {
		return nil, err
	}
	a.welcome = first.GetWelcome()
	go a.recv()
	return a, nil
}

func (f *fixture) connect(hello *pb.Hello) *agent {
	f.t.Helper()
	a, err := f.connectWith(f.token, hello)
	if err != nil {
		f.t.Fatalf("connect: %v", err)
	}
	return a
}

func (a *agent) recv() {
	for {
		m, err := a.stream.Recv()
		a.mu.Lock()
		if err != nil {
			a.err = err
			a.mu.Unlock()
			a.poke()
			return
		}
		switch b := m.GetBody().(type) {
		case *pb.ConnectResponse_Ack:
			a.acked = b.Ack.GetUpToSeq()
		case *pb.ConnectResponse_Settings:
			a.settings = append(a.settings, b.Settings)
		case *pb.ConnectResponse_Update:
			a.updates = append(a.updates, b.Update.GetVersion())
		case *pb.ConnectResponse_Command:
			a.commands = append(a.commands, b.Command)
			if a.answer != nil {
				if r := a.answer(b.Command); r != nil {
					_ = a.stream.Send(&pb.ConnectRequest{Body: &pb.ConnectRequest_CommandResult{CommandResult: r}})
				}
			}
		}
		a.mu.Unlock()
		a.poke()
	}
}

func (a *agent) poke() {
	select {
	case a.changed <- struct{}{}:
	default:
	}
}

func (a *agent) send(seqs ...uint64) {
	a.t.Helper()
	a.sendFrom("dev-a", seqs...)
}

func (a *agent) sendFrom(device string, seqs ...uint64) {
	a.t.Helper()
	batch := &pb.RecordBatch{}
	for _, s := range seqs {
		batch.Records = append(batch.Records, &pb.Record{Seq: s, DeviceId: device, Raw: []byte("r"),
			ReceivedAtUnixNano: time.Now().UnixNano()})
	}
	if err := a.stream.Send(&pb.ConnectRequest{Body: &pb.ConnectRequest_Records{Records: batch}}); err != nil {
		a.t.Fatalf("send: %v", err)
	}
}

func (a *agent) waitFor(what string, cond func() bool) {
	a.t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		a.mu.Lock()
		ok := cond()
		a.mu.Unlock()
		if ok {
			return
		}
		select {
		case <-a.changed:
		case <-time.After(10 * time.Millisecond):
		case <-deadline:
			a.t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func (a *agent) waitAcked(seq uint64) {
	a.t.Helper()
	a.waitFor("ack", func() bool { return a.acked >= seq })
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.acked != seq {
		a.t.Fatalf("acked = %d, want %d", a.acked, seq)
	}
}

// ackedNow reports the ack after giving the hub time to send one it should not.
func (a *agent) ackedNow() uint64 {
	time.Sleep(50 * time.Millisecond)
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.acked
}

func (a *agent) waitClosed() error {
	a.t.Helper()
	a.waitFor("stream end", func() bool { return a.err != nil })
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.err
}

func next(t *testing.T, sub *Subscription) Delivery {
	t.Helper()
	select {
	case d := <-sub.C():
		return d
	case <-time.After(5 * time.Second):
		t.Fatal("no delivery")
		return Delivery{}
	}
}

func nothing(t *testing.T, sub *Subscription) {
	t.Helper()
	select {
	case d := <-sub.C():
		t.Fatalf("unexpected delivery of seq %d", d.Record.GetSeq())
	case <-time.After(50 * time.Millisecond):
	}
}

func TestRefusesAConnectionWithoutAValidToken(t *testing.T) {
	f := newFixture(t)
	other, _, _ := storage.NewAgentToken("agt1")
	for name, token := range map[string]string{
		"none": "", "garbage": "garbage", "wrong secret": other, "unknown agent": "hag_nope_secret",
	} {
		_, err := f.connectWith(token, &pb.Hello{})
		if status.Code(err) != codes.Unauthenticated {
			t.Errorf("%s: err = %v, want Unauthenticated", name, err)
		}
	}
}

func TestWelcomesTheAgentSendsItsSettingsAndRecordsWhatItReports(t *testing.T) {
	f := newFixture(t)
	a := f.connect(&pb.Hello{Version: "1.2", Hostname: "pi", Os: "linux", Arch: "arm64"})
	if a.welcome.GetAgentId() != "agt1" {
		t.Fatalf("welcome = %v", a.welcome)
	}
	a.waitFor("settings", func() bool { return len(a.settings) == 1 })
	a.mu.Lock()
	got := a.settings[0].GetDevices()
	a.mu.Unlock()
	if len(got) != 2 || got[0].GetDeviceId() != "dev-a" || got[0].GetSerial().GetBaudRate() != 9600 ||
		got[1].GetTcp().GetMode() != pb.TcpMode_TCP_MODE_CLIENT {
		t.Fatalf("settings = %v", got)
	}

	if err := a.stream.Send(&pb.ConnectRequest{Body: &pb.ConnectRequest_Devices{Devices: &pb.DeviceList{
		Devices: []*pb.Device{{DeviceId: "usb:1:2:3", Kind: pb.DeviceKind_DEVICE_KIND_SERIAL, Port: "/dev/ttyUSB0", Present: true}},
	}}}); err != nil {
		t.Fatal(err)
	}
	if err := a.stream.Send(&pb.ConnectRequest{Body: &pb.ConnectRequest_Heartbeat{Heartbeat: &pb.Heartbeat{SpoolWaiting: 42}}}); err != nil {
		t.Fatal(err)
	}
	a.waitFor("status", func() bool {
		s := f.store.agent("agt1").Status
		return s.Spool.Waiting == 42 && len(s.Ports) == 1
	})
	s := f.store.agent("agt1").Status
	if s.Hostname != "pi" || s.Version != "1.2" || s.Arch != "arm64" || s.LastSeenAt.IsZero() ||
		s.Ports[0].Port != "/dev/ttyUSB0" || !s.Ports[0].Present {
		t.Fatalf("status = %+v", s)
	}
	if !f.hub.Live("agt1").Connected {
		t.Fatal("the hub does not report the agent connected")
	}
}

func TestAcknowledgesOnlyWhatTheWorkflowHasTakenInOrder(t *testing.T) {
	f := newFixture(t)
	sub := f.hub.Subscribe("agt1", "plant", nil)
	defer sub.Close()
	a := f.connect(&pb.Hello{})
	a.send(1, 2, 3)
	d1, d2, d3 := next(t, sub), next(t, sub), next(t, sub)
	if d1.Record.GetSeq() != 1 || d3.Record.GetSeq() != 3 || d1.DeviceName != "scale" || d1.AgentID != "agt1" {
		t.Fatalf("deliveries = %v %v", d1, d3)
	}
	if got := a.ackedNow(); got != 0 {
		t.Fatalf("acked %d before the workflow took anything", got)
	}
	sub.Done(d2.Record.GetSeq())
	if got := a.ackedNow(); got != 0 {
		t.Fatalf("acked %d past a record the workflow has not finished", got)
	}
	sub.Done(d1.Record.GetSeq())
	a.waitAcked(2)
	sub.Done(d3.Record.GetSeq())
	a.waitAcked(3)
}

func TestRecordsNoWorkflowReadsAreAcknowledgedAndCounted(t *testing.T) {
	f := newFixture(t)
	a := f.connect(&pb.Hello{})
	a.send(1, 2)
	a.waitAcked(2)
	if got := f.hub.Live("agt1").Unrouted; got != 2 {
		t.Fatalf("unrouted = %d, want 2", got)
	}
}

func TestOnlyTheAgentsVHostAndChosenDevicesAreDelivered(t *testing.T) {
	f := newFixture(t)
	other := f.hub.Subscribe("agt1", "office", nil)
	defer other.Close()
	onlyB := f.hub.Subscribe("agt1", "plant", []string{"dev-b"})
	defer onlyB.Close()
	a := f.connect(&pb.Hello{})
	a.sendFrom("dev-a", 1)
	a.sendFrom("dev-b", 2)
	d := next(t, onlyB)
	if d.Record.GetSeq() != 2 || d.DeviceName != "plc" {
		t.Fatalf("delivery = %+v", d)
	}
	nothing(t, onlyB)
	nothing(t, other)
	onlyB.Done(2)
	a.waitAcked(2)
}

func TestEveryWorkflowReadingADeviceMustFinishBeforeTheAck(t *testing.T) {
	f := newFixture(t)
	s1 := f.hub.Subscribe("agt1", "plant", nil)
	defer s1.Close()
	s2 := f.hub.Subscribe("agt1", "plant", []string{"dev-a"})
	defer s2.Close()
	a := f.connect(&pb.Hello{})
	a.send(1)
	next(t, s1)
	next(t, s2)
	s1.Done(1)
	if got := a.ackedNow(); got != 0 {
		t.Fatalf("acked %d with one workflow still on the record", got)
	}
	s1.Done(1) // twice from the same workflow counts once
	if got := a.ackedNow(); got != 0 {
		t.Fatalf("acked %d after a repeated Done", got)
	}
	s2.Done(1)
	a.waitAcked(1)
}

// A workflow that stops while it holds records never finishes them. The
// stream is ended so the agent sends them again, from its spool.
func TestAWorkflowStoppingWithRecordsInHandHasThemSentAgain(t *testing.T) {
	f := newFixture(t)
	sub := f.hub.Subscribe("agt1", "plant", nil)
	a := f.connect(&pb.Hello{})
	a.send(1, 2)
	next(t, sub)
	next(t, sub)
	sub.Done(1)
	a.waitAcked(1)
	sub.Close()
	if err := a.waitClosed(); status.Code(err) != codes.Aborted {
		t.Fatalf("stream ended with %v, want Aborted", err)
	}

	again := f.hub.Subscribe("agt1", "plant", nil)
	defer again.Close()
	b := f.connect(&pb.Hello{LastSeq: 2, AckedSeq: 1})
	if b.welcome.GetAckedSeq() != 1 {
		t.Fatalf("welcome acked = %d, want 1", b.welcome.GetAckedSeq())
	}
	b.send(2)
	if d := next(t, again); d.Record.GetSeq() != 2 {
		t.Fatalf("redelivered seq %d, want 2", d.Record.GetSeq())
	}
	again.Done(2)
	b.waitAcked(2)
}

// The agent resends what was not acknowledged when its stream breaks. What
// the workflow already holds is not delivered a second time.
func TestAReconnectDoesNotDeliverRecordsInHandTwice(t *testing.T) {
	f := newFixture(t)
	sub := f.hub.Subscribe("agt1", "plant", nil)
	defer sub.Close()
	a := f.connect(&pb.Hello{})
	a.send(1, 2)
	next(t, sub)
	next(t, sub)
	a.cancel()

	b := f.connect(&pb.Hello{LastSeq: 3})
	b.send(1, 2, 3)
	if d := next(t, sub); d.Record.GetSeq() != 3 {
		t.Fatalf("delivered seq %d, want only 3", d.Record.GetSeq())
	}
	nothing(t, sub)
	sub.Done(1)
	sub.Done(2)
	sub.Done(3)
	b.waitAcked(3)
}

// An agent whose spool was wiped starts counting again from 1. Its records
// are new, not duplicates of the old ones.
func TestAnAgentWhoseSequenceWentBackIsStartedAfresh(t *testing.T) {
	f := newFixture(t)
	sub := f.hub.Subscribe("agt1", "plant", nil)
	defer sub.Close()
	a := f.connect(&pb.Hello{})
	a.send(1, 2, 3)
	for range 3 {
		sub.Done(next(t, sub).Record.GetSeq())
	}
	a.waitAcked(3)
	a.cancel()

	b := f.connect(&pb.Hello{LastSeq: 1, AckedSeq: 0})
	if b.welcome.GetAckedSeq() != 0 {
		t.Fatalf("welcome acked = %d, want 0 for a fresh spool", b.welcome.GetAckedSeq())
	}
	b.send(1)
	if d := next(t, sub); d.Record.GetSeq() != 1 {
		t.Fatalf("delivered seq %d", d.Record.GetSeq())
	}
	sub.Done(1)
	b.waitAcked(1)
}

func TestANewConnectionReplacesTheOldOne(t *testing.T) {
	f := newFixture(t)
	a := f.connect(&pb.Hello{})
	f.connect(&pb.Hello{})
	if err := a.waitClosed(); err == nil || errors.Is(err, context.Canceled) {
		t.Fatalf("old stream ended with %v", err)
	}
}

func TestSettingsSavedWhileConnectedReachTheAgent(t *testing.T) {
	f := newFixture(t)
	a := f.connect(&pb.Hello{})
	a.waitFor("settings", func() bool { return len(a.settings) == 1 })
	devices := []storage.AgentDevice{{DeviceID: "dev-c", Kind: storage.AgentDeviceTCP, Name: "in",
		TCP: &storage.AgentTCP{Address: "0.0.0.0:9000", Mode: "listen"}}}
	if err := f.store.UpdateAgentDevices(t.Context(), "agt1", "line 1", devices); err != nil {
		t.Fatal(err)
	}
	f.hub.SettingsChanged(t.Context(), "agt1")
	a.waitFor("new settings", func() bool { return len(a.settings) == 2 })
	a.mu.Lock()
	got := a.settings[1].GetDevices()
	a.mu.Unlock()
	if len(got) != 1 || got[0].GetTcp().GetMode() != pb.TcpMode_TCP_MODE_LISTEN || got[0].GetEnabled() {
		t.Fatalf("settings = %v", got)
	}
}

func TestDisconnectEndsTheAgentsStream(t *testing.T) {
	f := newFixture(t)
	a := f.connect(&pb.Hello{})
	f.hub.Disconnect("agt1")
	if err := a.waitClosed(); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("stream ended with %v, want Unauthenticated", err)
	}
	if f.hub.Live("agt1").Connected {
		t.Fatal("the hub still reports the agent connected")
	}
}
