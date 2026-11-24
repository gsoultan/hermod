// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

//go:build integration

package rabbitmq

import (
	"fmt"
	"strings"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// ---------------------------------------------------------------------------
// The RabbitMQ sink publishing to an exchange, against a real broker. Each
// test checks where the message ended up, not only that Write returned nil:
// an exchange with nowhere to route a message drops it, and the broker still
// accepts the publish.
//
//	HERMOD_INTEGRATION=1 RABBITMQ_URL=amqp://guest:guest@127.0.0.1:5672/ \
//	go test -tags=integration ./pkg/comm/sink/rabbitmq/
// ---------------------------------------------------------------------------

type exchangeFixture struct {
	*rabbitSinkFixture
	exchange string
}

// newExchangeFixture reuses the queue fixture's broker connection, and names
// an exchange that is deleted when the test ends.
func newExchangeFixture(t *testing.T) *exchangeFixture {
	t.Helper()
	base := newRabbitSinkFixture(t)
	ex := fmt.Sprintf("hermod_ex_%x", time.Now().UnixNano())
	t.Cleanup(func() {
		ch, err := base.conn.Channel()
		if err == nil {
			_ = ch.ExchangeDelete(ex, false, false)
			_ = ch.Close()
		}
	})
	return &exchangeFixture{rabbitSinkFixture: base, exchange: ex}
}

// sink builds a sink from cfg, with this fixture's exchange and queue
// substituted for the words EXCHANGE and QUEUE.
func (f *exchangeFixture) sink(t *testing.T, cfg map[string]string) *RabbitMQQueueSink {
	t.Helper()
	for k, v := range cfg {
		cfg[k] = strings.NewReplacer("EXCHANGE", f.exchange, "QUEUE", f.queue).Replace(v)
	}
	s, err := ParseQueueSinkSettings(func(k string) string { return cfg[k] })
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	sink, err := NewRabbitMQQueueSinkWithSettings(f.url, s, nil)
	if err != nil {
		t.Fatalf("new sink: %v", err)
	}
	t.Cleanup(func() { _ = sink.Close() })
	return sink
}

// extraQueue declares a queue of the test's own, bound to the exchange that
// sink creates.
func (f *exchangeFixture) extraQueue(t *testing.T, sink *RabbitMQQueueSink, key string, args amqp.Table) string {
	t.Helper()
	if err := sink.Ping(t.Context()); err != nil {
		t.Fatalf("ping: %v", err)
	}
	ch, err := f.conn.Channel()
	if err != nil {
		t.Fatal(err)
	}
	defer ch.Close()
	q, err := ch.QueueDeclare("", false, true, true, false, nil)
	if err != nil {
		t.Fatalf("declare: %v", err)
	}
	if err := ch.QueueBind(q.Name, key, f.exchange, false, args); err != nil {
		t.Fatalf("bind: %v", err)
	}
	return q.Name
}

// consumeFrom returns the first message on queue, if one arrives within timeout.
func consumeFrom(t *testing.T, ch *amqp.Channel, queue string, timeout time.Duration) []amqp.Delivery {
	t.Helper()
	deliveries, err := ch.Consume(queue, "", true, false, false, false, nil)
	if err != nil {
		t.Fatalf("consume %s: %v", queue, err)
	}
	var got []amqp.Delivery
	deadline := time.After(timeout)
	for len(got) < 1 {
		select {
		case d, ok := <-deliveries:
			if !ok {
				return got
			}
			got = append(got, d)
		case <-deadline:
			return got
		}
	}
	return got
}

func TestADirectExchangeDeliversToTheQueueItBinds(t *testing.T) {
	f := newExchangeFixture(t)
	sink := f.sink(t, map[string]string{"exchange": "EXCHANGE", "exchange_type": "direct", "routing_key": "people", "queue_name": "QUEUE"})

	if err := sink.Write(t.Context(), rabbitMessage(t, "d-1", "Ada")); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := f.consume(t, 1, 10*time.Second)
	if len(got) != 1 || !strings.Contains(got[0], "Ada") {
		t.Fatalf("the bound queue received %v", got)
	}
}

// The routing key is a template, and the queue's binding pattern picks the
// records it wants.
func TestATopicExchangeRoutesEachRecordByItsRoutingKey(t *testing.T) {
	f := newExchangeFixture(t)
	sink := f.sink(t, map[string]string{
		"exchange": "EXCHANGE", "exchange_type": "topic",
		"routing_key": "db.{{.table}}", "binding_key": "db.people", "queue_name": "QUEUE",
	})
	other := f.extraQueue(t, sink, "db.#", nil)

	if err := sink.Write(t.Context(), rabbitMessage(t, "t-1", "Ada")); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := consumeFrom(t, f.ch, f.queue, 10*time.Second)
	if len(got) != 1 || got[0].RoutingKey != "db.people" {
		t.Fatalf("the queue bound to db.people received %d message(s); want one routed db.people", len(got))
	}
	if n := len(consumeFrom(t, f.ch, other, 10*time.Second)); n != 1 {
		t.Errorf("the queue bound to db.# received %d message(s), want 1", n)
	}
}

func TestAFanoutExchangeCopiesEachMessageToEveryBoundQueue(t *testing.T) {
	f := newExchangeFixture(t)
	sink := f.sink(t, map[string]string{"exchange": "EXCHANGE", "exchange_type": "fanout", "queue_name": "QUEUE"})
	other := f.extraQueue(t, sink, "", nil)

	if err := sink.Write(t.Context(), rabbitMessage(t, "f-1", "Ada")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if n := len(consumeFrom(t, f.ch, f.queue, 10*time.Second)); n != 1 {
		t.Errorf("the sink's own queue received %d, want 1", n)
	}
	if n := len(consumeFrom(t, f.ch, other, 10*time.Second)); n != 1 {
		t.Errorf("a second bound queue received %d, want 1", n)
	}
}

func TestAHeadersExchangeRoutesByTheMessageHeaders(t *testing.T) {
	f := newExchangeFixture(t)
	sink := f.sink(t, map[string]string{
		"exchange": "EXCHANGE", "exchange_type": "headers",
		"headers": "region=eu\ntable={{.table}}", "binding_headers": "region=eu", "queue_name": "QUEUE",
	})
	elsewhere := f.extraQueue(t, sink, "", amqp.Table{"x-match": "all", "region": "us"})

	if err := sink.Write(t.Context(), rabbitMessage(t, "h-1", "Ada")); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := consumeFrom(t, f.ch, f.queue, 10*time.Second)
	if len(got) != 1 {
		t.Fatalf("the queue bound to region=eu received %d, want 1", len(got))
	}
	if got[0].Headers["table"] != "people" {
		t.Errorf("templated header table=%v, want people", got[0].Headers["table"])
	}
	if n := len(consumeFrom(t, f.ch, elsewhere, 2*time.Second)); n != 0 {
		t.Errorf("the queue bound to region=us received %d, want 0", n)
	}
}

// An exchange with no queue bound drops a message and still accepts the
// publish. The sink must report that write as failed, or the record is lost
// with nothing in the logs.
func TestAMessageNoQueueReceivesIsAFailedWrite(t *testing.T) {
	f := newExchangeFixture(t)
	sink := f.sink(t, map[string]string{"exchange": "EXCHANGE", "exchange_type": "direct", "routing_key": "nobody"})

	err := sink.Write(t.Context(), rabbitMessage(t, "u-1", "Ada"))
	if err == nil {
		t.Fatal("a message no queue received was reported as written")
	}
	if !strings.Contains(err.Error(), "no queue") {
		t.Errorf("error %q does not say no queue received it", err)
	}

	// The next write, once a queue is bound, goes through on the same sink.
	q := f.extraQueue(t, sink, "nobody", nil)
	if err := sink.Write(t.Context(), rabbitMessage(t, "u-2", "Grace")); err != nil {
		t.Fatalf("write after binding: %v", err)
	}
	if n := len(consumeFrom(t, f.ch, q, 10*time.Second)); n != 1 {
		t.Errorf("received %d after binding, want 1", n)
	}
}

func TestAnExchangeOfAnotherTypeIsNamedInTheError(t *testing.T) {
	f := newExchangeFixture(t)
	if err := f.ch.ExchangeDeclare(f.exchange, "fanout", true, false, false, false, nil); err != nil {
		t.Fatal(err)
	}
	sink := f.sink(t, map[string]string{"exchange": "EXCHANGE", "exchange_type": "topic", "routing_key": "a.b"})

	err := sink.Ping(t.Context())
	if err == nil {
		t.Fatal("a fanout exchange was accepted as topic")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error %q does not say the exchange exists with other settings", err)
	}
}

func TestAnExchangeThatMustExistIsCheckedNotCreated(t *testing.T) {
	f := newExchangeFixture(t)
	sink := f.sink(t, map[string]string{"exchange": "EXCHANGE", "exchange_type": "fanout", "exchange_declare": "false"})

	err := sink.Ping(t.Context())
	if err == nil {
		t.Fatal("a missing exchange passed the check")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("error %q does not say the exchange is missing", err)
	}
}

func TestABuiltInExchangeIsUsedWithoutDeclaringIt(t *testing.T) {
	f := newExchangeFixture(t)
	f.exchange = "amq.topic"
	sink := f.sink(t, map[string]string{"exchange": "EXCHANGE", "exchange_type": "topic", "routing_key": "hermod.{{.table}}", "binding_key": "hermod.*", "queue_name": "QUEUE"})

	if err := sink.Write(t.Context(), rabbitMessage(t, "a-1", "Ada")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if n := len(f.consume(t, 1, 10*time.Second)); n != 1 {
		t.Errorf("received %d through amq.topic, want 1", n)
	}
}

// RabbitMQ closes the channel, not the connection, when a publish names an
// exchange that is gone. The sink must open a new channel, and set the
// exchange up again, rather than fail every write after it.
func TestAWriteAfterTheExchangeWasDeletedRecovers(t *testing.T) {
	f := newExchangeFixture(t)
	sink := f.sink(t, map[string]string{"exchange": "EXCHANGE", "exchange_type": "direct", "routing_key": "people", "queue_name": "QUEUE"})
	if err := sink.Write(t.Context(), rabbitMessage(t, "r-1", "Ada")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := f.ch.ExchangeDelete(f.exchange, false, false); err != nil {
		t.Fatal(err)
	}

	if err := sink.Write(t.Context(), rabbitMessage(t, "r-2", "Grace")); err == nil {
		t.Fatal("a publish to a deleted exchange was reported as written")
	}
	if err := sink.Write(t.Context(), rabbitMessage(t, "r-3", "Alan")); err != nil {
		t.Fatalf("the write after it still failed: %v", err)
	}
	got := f.consume(t, 2, 10*time.Second)
	if len(got) != 2 || !strings.Contains(got[1], "Alan") {
		t.Errorf("queue holds %v, want Ada then Alan", got)
	}
}
