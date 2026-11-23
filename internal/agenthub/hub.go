// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

// Package agenthub is Hermod's end of hermod-agent: the gRPC service agents
// connect to, and the hand-off of what they read to the workflows that read
// their devices.
//
// An agent streams records from its disk spool and deletes them when Hermod
// acknowledges them. Hermod acknowledges a record only once every workflow
// reading its device has finished with it, so a record is never lost between
// the agent's disk and the workflow: until then it is still in the spool, and
// a broken stream or a stopped workflow has the agent send it again. Delivery
// is at least once.
//
// Agents and the workflows that read them meet in this process. An agent
// connects to one Hermod, and its records reach the workflows running there.
package agenthub

import (
	"context"
	"crypto/subtle"
	"errors"
	"io"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	pb "github.com/gsoultan/hermod/pkg/agent/proto"
)

// subscriptionBuffer is how many records a workflow may have waiting. It is
// the agent's window: the agent never has more than that unacknowledged.
const subscriptionBuffer = 4096

// statusTimeout bounds saving what an agent reported.
const statusTimeout = 5 * time.Second

// Why a stream is ended from this side. The agent reconnects after each.
var (
	errReplaced  = status.Error(codes.Aborted, "another connection of this agent replaced this one")
	errResend    = status.Error(codes.Aborted, "a workflow stopped before it finished records; send them again")
	errRevoked   = status.Error(codes.Unauthenticated, "the agent was deleted or its token replaced")
	errBadToken  = status.Error(codes.Unauthenticated, "invalid agent token")
	errNoStore   = status.Error(codes.Unavailable, "Hermod has no database that can hold agents yet")
	errNeedHello = status.Error(codes.InvalidArgument, "the first message must be Hello")
)

// Hub is the agent service, and where workflows subscribe to agents' devices.
type Hub struct {
	pb.UnimplementedAgentServiceServer

	store func() any
	log   hermod.Logger

	mu     sync.Mutex
	agents map[string]*agentState
}

// agentState is what the hub knows of one agent, connected or not.
type agentState struct {
	vhost string
	names map[string]string // device id -> name

	ledger ledger
	sess   *session
	subs   map[*Subscription]struct{}

	unrouted    uint64
	connectedAt time.Time
	status      storage.AgentStatus
}

// session is one stream from an agent.
type session struct {
	cancel   context.CancelCauseFunc
	wake     chan struct{} // an ack or new settings to send
	settings *pb.DeviceSettingsList
	sentAck  uint64
}

func (s *session) poke() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// LiveStatus is what the hub knows of an agent right now.
type LiveStatus struct {
	Connected   bool      `json:"connected"`
	ConnectedAt time.Time `json:"connected_at,omitzero"`
	// Unrouted counts records no workflow was reading when they arrived.
	// They are acknowledged, so the agent does not keep them.
	Unrouted uint64 `json:"unrouted"`
	// Workflows is how many workflows read the agent here.
	Workflows int `json:"workflows"`
	// Acked is the agent's sequence number up to which every record is
	// finished and the agent told so.
	Acked uint64 `json:"acked"`
}

// New returns a hub. store returns the current metadata store, which holds
// the agents when it is a storage.AgentStore; log may be nil.
func New(store func() any, log hermod.Logger) *Hub {
	if store == nil {
		store = func() any { return nil }
	}
	return &Hub{store: store, log: log, agents: map[string]*agentState{}}
}

// Default is the hub of this process. The API server stores and serves it,
// and the Agent source subscribes to it.
var Default = New(nil, nil)

// SetStore sets where the hub finds agents.
func (h *Hub) SetStore(store func() any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.store = store
}

// SetLogger sets the hub's logger.
func (h *Hub) SetLogger(log hermod.Logger) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.log = log
}

func (h *Hub) agentStore() (storage.AgentStore, bool) {
	h.mu.Lock()
	store := h.store
	h.mu.Unlock()
	as, ok := store().(storage.AgentStore)
	return as, ok
}

func (h *Hub) warn(msg string, kv ...any) {
	h.mu.Lock()
	log := h.log
	h.mu.Unlock()
	if log != nil {
		log.Warn(msg, kv...)
	}
}

// state returns the agent's state, creating it. h.mu must be held.
func (h *Hub) state(id string) *agentState {
	st, ok := h.agents[id]
	if !ok {
		st = &agentState{subs: map[*Subscription]struct{}{}}
		h.agents[id] = st
	}
	return st
}

// Live reports what the hub knows of the agent now.
func (h *Hub) Live(id string) LiveStatus {
	h.mu.Lock()
	defer h.mu.Unlock()
	st, ok := h.agents[id]
	if !ok {
		return LiveStatus{}
	}
	return LiveStatus{
		Connected: st.sess != nil, ConnectedAt: st.connectedAt, Unrouted: st.unrouted, Workflows: len(st.subs),
		Acked: st.ledger.acked,
	}
}

// Disconnect ends the agent's stream, as when it is deleted or its token is
// replaced: it is asked for its token again when it reconnects.
func (h *Hub) Disconnect(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if st, ok := h.agents[id]; ok && st.sess != nil {
		st.sess.cancel(errRevoked)
	}
}

// SettingsChanged sends the agent its saved settings, if it is connected
// here, and takes up the devices' new names.
func (h *Hub) SettingsChanged(ctx context.Context, id string) {
	as, ok := h.agentStore()
	if !ok {
		return
	}
	a, err := as.GetAgent(ctx, id)
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	st := h.state(id)
	st.vhost, st.names = a.VHost, deviceNames(a.Devices)
	if st.sess != nil {
		st.sess.settings = settingsList(a.Devices)
		st.sess.poke()
	}
}

func deviceNames(devices []storage.AgentDevice) map[string]string {
	names := make(map[string]string, len(devices))
	for _, d := range devices {
		names[d.DeviceID] = d.Name
	}
	return names
}

// Connect serves one agent's stream.
func (h *Hub) Connect(stream grpc.BidiStreamingServer[pb.ConnectRequest, pb.ConnectResponse]) error {
	ctx := stream.Context()
	a, err := h.authenticate(ctx)
	if err != nil {
		return err
	}
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	hello := first.GetHello()
	if hello == nil {
		return errNeedHello
	}

	sctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	sess := &session{cancel: cancel, wake: make(chan struct{}, 1), settings: settingsList(a.Devices)}
	acked := h.attach(a, hello, sess)
	defer h.detach(a.ID, sess)

	welcome := &pb.Welcome{AgentId: a.ID, AckedSeq: acked}
	if err := stream.Send(&pb.ConnectResponse{Body: &pb.ConnectResponse_Welcome{Welcome: welcome}}); err != nil {
		return err
	}
	sess.poke()
	h.report(ctx, a.ID, func(s *storage.AgentStatus) {
		s.Hostname, s.OS, s.Arch, s.Version = hello.GetHostname(), hello.GetOs(), hello.GetArch(), hello.GetVersion()
	})

	go h.sendLoop(sctx, stream, a.ID, sess)
	go func() { cancel(h.recvLoop(sctx, stream, a.ID, sess)) }()
	<-sctx.Done()
	return streamResult(context.Cause(sctx))
}

// streamResult is what Connect returns for why the stream ended.
func streamResult(cause error) error {
	if errors.Is(cause, io.EOF) {
		return nil
	}
	if _, ok := status.FromError(cause); ok {
		return cause
	}
	return status.FromContextError(cause).Err()
}

// authenticate finds the agent the stream's bearer token belongs to. Every
// way a token can be wrong gets the same answer.
func (h *Hub) authenticate(ctx context.Context) (storage.Agent, error) {
	as, ok := h.agentStore()
	if !ok {
		return storage.Agent{}, errNoStore
	}
	md, _ := metadata.FromIncomingContext(ctx)
	values := md.Get("authorization")
	if len(values) != 1 {
		return storage.Agent{}, errBadToken
	}
	token, ok := strings.CutPrefix(values[0], "Bearer ")
	if !ok {
		return storage.Agent{}, errBadToken
	}
	id, ok := storage.AgentIDFromToken(token)
	if !ok {
		return storage.Agent{}, errBadToken
	}
	a, err := as.GetAgent(ctx, id)
	if errors.Is(err, storage.ErrNotFound) {
		return storage.Agent{}, errBadToken
	}
	if err != nil {
		return storage.Agent{}, status.Error(codes.Unavailable, "agents cannot be read right now")
	}
	if subtle.ConstantTimeCompare([]byte(storage.HashAgentToken(token)), []byte(a.TokenHash)) != 1 {
		return storage.Agent{}, errBadToken
	}
	return a, nil
}

// attach makes sess the agent's stream, ending any other, and returns the
// ack the agent is welcomed with.
func (h *Hub) attach(a storage.Agent, hello *pb.Hello, sess *session) uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	st := h.state(a.ID)
	if st.sess != nil {
		st.sess.cancel(errReplaced)
		h.warnLocked("an agent connected while it was already connected; if this repeats, two agents share a token",
			"agent_id", a.ID)
	}
	st.sess, st.connectedAt = sess, time.Now().UTC()
	st.vhost, st.names = a.VHost, deviceNames(a.Devices)
	st.status = a.Status

	l := &st.ledger
	switch {
	case hello.GetLastSeq() < l.highest:
		// The agent counts from where its spool is. Counting from lower
		// than what was accepted means a new spool: its records are new.
		l.reset(hello.GetAckedSeq())
	case hello.GetAckedSeq() > l.acked:
		// An earlier Hermod acknowledged these.
		for len(l.queue) > 0 && l.queue[0].seq <= hello.GetAckedSeq() {
			l.queue = l.queue[1:]
		}
		l.acked, l.highest = hello.GetAckedSeq(), max(l.highest, hello.GetAckedSeq())
	}
	sess.sentAck = l.acked
	return l.acked
}

func (h *Hub) warnLocked(msg string, kv ...any) {
	if h.log != nil {
		h.log.Warn(msg, kv...)
	}
}

func (h *Hub) detach(id string, sess *session) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if st, ok := h.agents[id]; ok && st.sess == sess {
		st.sess = nil
	}
}

// report changes the agent's status and saves it.
func (h *Hub) report(ctx context.Context, id string, change func(*storage.AgentStatus)) {
	h.mu.Lock()
	st := h.state(id)
	change(&st.status)
	st.status.LastSeenAt = time.Now().UTC()
	snapshot := st.status
	h.mu.Unlock()

	as, ok := h.agentStore()
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), statusTimeout)
	defer cancel()
	if err := as.UpdateAgentStatus(ctx, id, snapshot); err != nil && !errors.Is(err, storage.ErrNotFound) {
		h.warn("could not save what an agent reported", "agent_id", id, "error", err)
	}
}

// sendLoop owns the stream's sending side: settings and acks.
func (h *Hub) sendLoop(ctx context.Context, stream grpc.BidiStreamingServer[pb.ConnectRequest, pb.ConnectResponse], id string, sess *session) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-sess.wake:
		}
		settings, ack := h.pending(id, sess)
		if settings != nil {
			if err := stream.Send(&pb.ConnectResponse{Body: &pb.ConnectResponse_Settings{Settings: settings}}); err != nil {
				sess.cancel(err)
				return
			}
		}
		if ack != nil {
			if err := stream.Send(&pb.ConnectResponse{Body: &pb.ConnectResponse_Ack{Ack: ack}}); err != nil {
				sess.cancel(err)
				return
			}
		}
	}
}

// pending takes what sess has to send.
func (h *Hub) pending(id string, sess *session) (*pb.DeviceSettingsList, *pb.Ack) {
	h.mu.Lock()
	defer h.mu.Unlock()
	settings := sess.settings
	sess.settings = nil
	st, ok := h.agents[id]
	if !ok || st.sess != sess || st.ledger.acked == sess.sentAck {
		return settings, nil
	}
	sess.sentAck = st.ledger.acked
	return settings, &pb.Ack{UpToSeq: st.ledger.acked}
}

// recvLoop handles what the agent sends until the stream ends, and returns
// why it ended.
func (h *Hub) recvLoop(ctx context.Context, stream grpc.BidiStreamingServer[pb.ConnectRequest, pb.ConnectResponse], id string, sess *session) error {
	for {
		m, err := stream.Recv()
		if err != nil {
			return err
		}
		switch b := m.GetBody().(type) {
		case *pb.ConnectRequest_Records:
			if err := h.records(ctx, id, sess, b.Records.GetRecords()); err != nil {
				return err
			}
		case *pb.ConnectRequest_Devices:
			found := ports(b.Devices.GetDevices())
			h.report(ctx, id, func(s *storage.AgentStatus) { s.Ports = found })
		case *pb.ConnectRequest_Heartbeat:
			spool := spoolOf(b.Heartbeat)
			h.report(ctx, id, func(s *storage.AgentStatus) { s.Spool = spool })
		}
	}
}

func spoolOf(hb *pb.Heartbeat) storage.AgentSpool {
	s := storage.AgentSpool{
		Waiting: hb.GetSpoolWaiting(), Bytes: hb.GetSpoolBytes(),
		Dropped: hb.GetSpoolDropped(), OversizeDropped: hb.GetOversizeDropped(),
	}
	if ms := hb.GetSpoolOldestUnixMs(); ms > 0 {
		s.OldestAt = time.UnixMilli(ms).UTC()
	}
	return s
}

// records hands each new record to the workflows reading its device.
func (h *Hub) records(ctx context.Context, id string, sess *session, recs []*pb.Record) error {
	for _, rec := range recs {
		d, subs := h.accept(id, sess, rec)
		for _, sub := range subs {
			select {
			case sub.ch <- d:
			case <-sub.done:
				// It stopped holding this record, and has ended this
				// stream so the agent sends it again.
			case <-ctx.Done():
				return context.Cause(ctx)
			}
		}
	}
	return nil
}

// accept records rec in the agent's ledger and returns the workflows to hand
// it to. A record no workflow reads is finished at once.
func (h *Hub) accept(id string, sess *session, rec *pb.Record) (Delivery, []*Subscription) {
	h.mu.Lock()
	defer h.mu.Unlock()
	st, ok := h.agents[id]
	if !ok || st.sess != sess {
		return Delivery{}, nil
	}
	var subs []*Subscription
	for sub := range st.subs {
		if sub.wants(st.vhost, rec.GetDeviceId()) {
			subs = append(subs, sub)
		}
	}
	if !st.ledger.accept(rec.GetSeq(), subs) {
		return Delivery{}, nil
	}
	if len(subs) == 0 {
		st.unrouted++
		if st.ledger.advance() {
			sess.poke()
		}
	}
	return Delivery{AgentID: id, DeviceName: st.names[rec.GetDeviceId()], Record: rec}, subs
}

// Delivery is one record for a workflow.
type Delivery struct {
	AgentID    string
	DeviceName string
	Record     *pb.Record
}

// Subscription is one workflow reading an agent's devices.
type Subscription struct {
	hub     *Hub
	agentID string
	vhost   string
	devices map[string]bool

	ch        chan Delivery
	done      chan struct{}
	closeOnce sync.Once
}

// Subscribe starts handing the workflow the records of the agent's devices:
// the ones named, or all of them when none are. vhost is the workflow's; an
// agent of another vhost is never delivered.
func (h *Hub) Subscribe(agentID, vhost string, deviceIDs []string) *Subscription {
	sub := &Subscription{
		hub: h, agentID: agentID, vhost: normVHost(vhost), devices: map[string]bool{},
		ch: make(chan Delivery, subscriptionBuffer), done: make(chan struct{}),
	}
	for _, d := range deviceIDs {
		sub.devices[d] = true
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.state(agentID).subs[sub] = struct{}{}
	return sub
}

func normVHost(v string) string {
	if v == "" {
		return "default"
	}
	return v
}

func (s *Subscription) wants(agentVHost, deviceID string) bool {
	if agentVHost == "" || normVHost(agentVHost) != s.vhost {
		return false
	}
	return len(s.devices) == 0 || s.devices[deviceID]
}

// C delivers the records.
func (s *Subscription) C() <-chan Delivery { return s.ch }

// Done records that the workflow has finished with seq.
func (s *Subscription) Done(seq uint64) {
	h := s.hub
	h.mu.Lock()
	defer h.mu.Unlock()
	st, ok := h.agents[s.agentID]
	if !ok {
		return
	}
	if st.ledger.done(s, seq) && st.sess != nil {
		st.sess.poke()
	}
}

// Close stops the subscription. Records it had not finished are sent again
// by the agent: the stream is ended so it resends from its last ack.
func (s *Subscription) Close() {
	s.closeOnce.Do(func() {
		close(s.done)
		h := s.hub
		h.mu.Lock()
		defer h.mu.Unlock()
		st, ok := h.agents[s.agentID]
		if !ok {
			return
		}
		delete(st.subs, s)
		if st.ledger.holds(s) {
			st.ledger.reset(st.ledger.acked)
			if st.sess != nil {
				st.sess.cancel(errResend)
			}
		}
	})
}
