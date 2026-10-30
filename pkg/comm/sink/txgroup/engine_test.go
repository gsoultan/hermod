// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package txgroup_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/buffer"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/comm/sink/txgroup"
	"github.com/gsoultan/hermod/pkg/engine"
	"github.com/gsoultan/hermod/pkg/engine/twopc"
)

// A group driven by the engine, end to end: what reaches each database when an
// event is incomplete, and when one database refuses its row.

// db is a member that keeps what a real database would: rows written inside a
// transaction become durable when it commits, rows written outside one at once.
type db struct {
	mu      sync.Mutex
	inTx    bool
	pending []string
	durable []string
	refuse  string
}

func (d *db) Write(ctx context.Context, m hermod.Message) error {
	return d.WriteBatch(ctx, []hermod.Message{m})
}

func (d *db) WriteBatch(_ context.Context, msgs []hermod.Message) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, m := range msgs {
		if m.ID() == d.refuse {
			return errors.Join(hermod.ErrPermanent, errors.New("CHECK constraint failed"))
		}
	}
	for _, m := range msgs {
		if d.inTx {
			d.pending = append(d.pending, m.ID())
		} else {
			d.durable = append(d.durable, m.ID())
		}
	}
	return nil
}

func (d *db) Ping(context.Context) error { return nil }
func (d *db) Close() error               { return nil }

func (d *db) Begin(context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.inTx, d.pending = true, nil
	return nil
}
func (d *db) Commit(context.Context) error { return d.CommitPrepared(context.Background(), "") }
func (d *db) Rollback(context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.inTx, d.pending = false, nil
	return nil
}
func (d *db) Prepare(_ context.Context, txID string) (string, error) { return txID, nil }
func (d *db) CommitPrepared(context.Context, string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.durable = append(d.durable, d.pending...)
	d.inTx, d.pending = false, nil
	return nil
}
func (d *db) RollbackPrepared(ctx context.Context, _ string) error { return d.Rollback(ctx) }

func (d *db) rows() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.durable...)
}

type memKV struct {
	mu sync.Mutex
	kv map[string][]byte
}

func (m *memKV) Get(_ context.Context, k string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.kv[k], nil
}
func (m *memKV) Set(_ context.Context, k string, v []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.kv[k] = v
	return nil
}
func (m *memKV) Delete(_ context.Context, k string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.kv, k)
	return nil
}

type oneEvent struct {
	mu    sync.Mutex
	sent  bool
	acked int
}

func (s *oneEvent) Read(ctx context.Context) (hermod.Message, error) {
	s.mu.Lock()
	if s.sent {
		s.mu.Unlock()
		<-ctx.Done()
		return nil, ctx.Err()
	}
	s.sent = true
	s.mu.Unlock()
	m := message.AcquireMessage()
	m.SetID("ev")
	m.SetPayload([]byte(`{}`))
	return m, nil
}
func (s *oneEvent) Ack(context.Context, hermod.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.acked++
	return nil
}
func (s *oneEvent) Ping(context.Context) error { return nil }
func (s *oneEvent) Close() error               { return nil }

type parking struct {
	mu     sync.Mutex
	parked []string
	first  chan struct{}
	once   sync.Once
}

func (p *parking) Write(_ context.Context, m hermod.Message) error {
	p.mu.Lock()
	p.parked = append(p.parked, m.ID())
	p.mu.Unlock()
	p.once.Do(func() { close(p.first) })
	return nil
}
func (p *parking) Ping(context.Context) error { return nil }
func (p *parking) Close() error               { return nil }

// rowsFor routes the event to the group once per named node, tagged as the
// traversal tags it.
func rowsFor(nodes ...string) engine.RouterFunc {
	return func(_ context.Context, msg hermod.Message) ([]engine.RoutedMessage, error) {
		out := make([]engine.RoutedMessage, 0, len(nodes))
		for _, n := range nodes {
			c := message.AcquireMessage()
			c.SetID(msg.ID() + "@" + n)
			c.SetPayload(msg.Payload())
			c.SetMetadata(hermod.MetaSinkNode, n)
			out = append(out, engine.RoutedMessage{SinkIndex: 0, Message: c})
		}
		return out, nil
	}
}

func runGroup(t *testing.T, group *txgroup.Sink, router engine.RouterFunc) (*oneEvent, *parking) {
	t.Helper()
	src := &oneEvent{}
	dlq := &parking{first: make(chan struct{})}
	eng := engine.NewEngine(src, []hermod.Sink{group}, buffer.NewRingBuffer(8))
	eng.SetConfig(engine.Config{MaxRetries: 1, RetryInterval: time.Millisecond, StatusInterval: time.Second})
	eng.SetSinkConfigs([]engine.SinkConfig{{BatchSize: 1, BatchTimeout: time.Hour}})
	eng.SetDeadLetterSink(dlq)
	eng.SetRouter(router)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = eng.Start(ctx)
	}()
	select {
	case <-dlq.first:
	case <-time.After(3 * time.Second):
		t.Fatal("nothing was parked")
	}
	time.Sleep(200 * time.Millisecond)
	cancel()
	<-done
	return src, dlq
}

func newEngineGroup(t *testing.T, a, b *db) *txgroup.Sink {
	t.Helper()
	c, err := twopc.New(twopc.Options{Store: twopc.NewIndexedStore(&memKV{kv: map[string][]byte{}}, "idx"), WorkflowID: "wf/order"})
	if err != nil {
		t.Fatal(err)
	}
	g, err := txgroup.New([]txgroup.Member{
		{ID: "pg", Name: "Orders", Sink: a},
		{ID: "my", Name: "Invoices", Sink: b},
	}, c, nil)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestAnEventMissingADatabasesRowIsParkedAndWritesNothing(t *testing.T) {
	a, b := &db{}, &db{}
	g := newEngineGroup(t, a, b)
	g.SetRequireEveryMember(true)

	src, dlq := runGroup(t, g, rowsFor("pg"))

	if rows := a.rows(); len(rows) != 0 {
		t.Errorf("Orders kept %v for an event Invoices got no row of", rows)
	}
	if len(dlq.parked) != 1 {
		t.Errorf("parked %v, want the event's one row", dlq.parked)
	}
	if src.acked != 1 {
		t.Errorf("acknowledged %d time(s), want once: the event is parked", src.acked)
	}
	if st := g.Stats(); st.Refused != 1 || st.Committed != 0 {
		t.Errorf("stats %+v, want one refused run", st)
	}
}

func TestADatabaseRefusingItsRowLeavesTheOtherEmptyThroughTheEngine(t *testing.T) {
	a, b := &db{}, &db{refuse: "ev@my"}
	g := newEngineGroup(t, a, b)

	src, dlq := runGroup(t, g, rowsFor("pg", "my"))

	if rows := a.rows(); len(rows) != 0 {
		t.Errorf("Orders kept %v although Invoices refused the event's other row", rows)
	}
	if len(dlq.parked) != 2 {
		t.Errorf("parked %v, want both rows of the event", dlq.parked)
	}
	if src.acked != 1 {
		t.Errorf("acknowledged %d time(s), want once", src.acked)
	}
}
