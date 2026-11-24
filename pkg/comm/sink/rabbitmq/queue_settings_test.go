// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package rabbitmq

import (
	"strings"
	"testing"
)

func settingsFrom(cfg map[string]string) (QueueSinkSettings, error) {
	return ParseQueueSinkSettings(func(k string) string { return cfg[k] })
}

// A sink saved before exchanges existed has a queue name and nothing else. It
// must keep publishing straight to that queue through the default exchange.
func TestAQueueOnlySinkStillPublishesToItsQueue(t *testing.T) {
	s, err := settingsFrom(map[string]string{"queue_name": "orders"})
	if err != nil {
		t.Fatalf("a queue-only sink was refused: %v", err)
	}
	if s.Exchange != "" || s.Queue != "orders" {
		t.Fatalf("got exchange %q queue %q, want the default exchange and queue orders", s.Exchange, s.Queue)
	}
	if s.RoutingKey != "orders" {
		t.Errorf("routing key %q, want the queue name: the default exchange routes by it", s.RoutingKey)
	}
}

func TestEachExchangeTypeIsAccepted(t *testing.T) {
	cases := map[string]map[string]string{
		"direct":  {"exchange": "ex", "exchange_type": "direct", "routing_key": "orders"},
		"topic":   {"exchange": "ex", "exchange_type": "topic", "routing_key": "orders.{{.operation}}"},
		"fanout":  {"exchange": "ex", "exchange_type": "fanout"},
		"headers": {"exchange": "ex", "exchange_type": "headers", "headers": "region=eu"},
	}
	for typ, cfg := range cases {
		t.Run(typ, func(t *testing.T) {
			s, err := settingsFrom(cfg)
			if err != nil {
				t.Fatalf("refused: %v", err)
			}
			if s.ExchangeType != typ {
				t.Errorf("exchange type %q, want %q", s.ExchangeType, typ)
			}
			if !s.DeclareExchange {
				t.Error("an exchange is created when missing unless the user says otherwise")
			}
		})
	}
}

// What each refusal must name, so the form's message points at the field.
func TestMisconfigurationsAreRefusedBeforeAnythingIsSent(t *testing.T) {
	cases := []struct {
		name string
		cfg  map[string]string
		want string
	}{
		{"nothing to send to", map[string]string{}, "queue name or an exchange"},
		{"unknown type", map[string]string{"exchange": "ex", "exchange_type": "round-robin"}, "not one of direct, fanout, topic, headers"},
		{"direct without a routing key", map[string]string{"exchange": "ex", "exchange_type": "direct"}, "routing key"},
		{"topic without a routing key", map[string]string{"exchange": "ex", "exchange_type": "topic"}, "routing key"},
		{"topic routing key with a wildcard", map[string]string{"exchange": "ex", "exchange_type": "topic", "routing_key": "orders.*"}, "binding key"},
		{"headers without headers", map[string]string{"exchange": "ex", "exchange_type": "headers"}, "at least one header"},
		{"malformed header", map[string]string{"exchange": "ex", "exchange_type": "headers", "headers": "region"}, "key=value"},
		{"bad match", map[string]string{"exchange": "ex", "exchange_type": "headers", "headers": "a=b", "headers_match": "some"}, "all or any"},
		{"templated routing key binding a queue", map[string]string{"exchange": "ex", "exchange_type": "direct", "routing_key": "{{.table}}", "queue_name": "q"}, "binding key"},
		{"templated header binding a queue", map[string]string{"exchange": "ex", "exchange_type": "headers", "headers": "t={{.table}}", "queue_name": "q"}, "binding headers"},
		{"reserved default exchange name", map[string]string{"exchange": "amq.default", "exchange_type": "direct", "routing_key": "k"}, "leave the exchange empty"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := settingsFrom(c.cfg)
			if err == nil {
				t.Fatal("accepted")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q does not mention %q", err, c.want)
			}
		})
	}
}

func TestTheExchangeTypeDefaultsToDirect(t *testing.T) {
	s, err := settingsFrom(map[string]string{"exchange": "ex", "routing_key": "k"})
	if err != nil {
		t.Fatal(err)
	}
	if s.ExchangeType != "direct" {
		t.Errorf("type %q, want direct, RabbitMQ's own default", s.ExchangeType)
	}
}

// A bound queue needs a binding key. When the routing key is fixed, binding
// with the same key is what delivers every message to the queue.
func TestTheBindingKeyDefaultsToAFixedRoutingKey(t *testing.T) {
	s, err := settingsFrom(map[string]string{"exchange": "ex", "exchange_type": "topic", "routing_key": "orders.created", "queue_name": "q"})
	if err != nil {
		t.Fatal(err)
	}
	if s.BindingKey != "orders.created" {
		t.Errorf("binding key %q, want the routing key", s.BindingKey)
	}

	s, err = settingsFrom(map[string]string{"exchange": "ex", "exchange_type": "topic", "routing_key": "orders.{{.operation}}", "binding_key": "orders.#", "queue_name": "q"})
	if err != nil {
		t.Fatal(err)
	}
	if s.BindingKey != "orders.#" {
		t.Errorf("binding key %q, want the one given", s.BindingKey)
	}
}

func TestHeadersBindWithTheirOwnValuesAndMatchAll(t *testing.T) {
	s, err := settingsFrom(map[string]string{"exchange": "ex", "exchange_type": "headers", "headers": "region=eu\n kind = order ", "queue_name": "q"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Headers["region"] != "eu" || s.Headers["kind"] != "order" || len(s.Headers) != 2 {
		t.Errorf("headers %v", s.Headers)
	}
	if s.BindingHeaders["region"] != "eu" || len(s.BindingHeaders) != 2 {
		t.Errorf("binding headers %v, want the published headers", s.BindingHeaders)
	}
	if s.HeadersMatch != "all" {
		t.Errorf("match %q, want all", s.HeadersMatch)
	}
}

// Fields for another exchange type can be left over after the user switched
// type in the form. They are ignored, not refused.
func TestFieldsForAnotherTypeAreIgnored(t *testing.T) {
	s, err := settingsFrom(map[string]string{"exchange": "ex", "exchange_type": "fanout", "routing_key": "orders.*", "headers": "junk"})
	if err != nil {
		t.Fatalf("refused leftover fields: %v", err)
	}
	if s.RoutingKey != "" || len(s.Headers) != 0 {
		t.Errorf("fanout kept routing key %q and headers %v", s.RoutingKey, s.Headers)
	}
}

func TestAnExistingExchangeCanBeUsedWithoutDeclaringIt(t *testing.T) {
	s, err := settingsFrom(map[string]string{"exchange": "ex", "exchange_type": "fanout", "exchange_declare": "false"})
	if err != nil {
		t.Fatal(err)
	}
	if s.DeclareExchange {
		t.Error("exchange_declare=false still declares the exchange")
	}
	// The built-in amq.* exchanges cannot be declared by a client.
	s, err = settingsFrom(map[string]string{"exchange": "amq.topic", "exchange_type": "topic", "routing_key": "a.b"})
	if err != nil {
		t.Fatal(err)
	}
	if s.DeclareExchange {
		t.Error("amq.topic is declared; RabbitMQ refuses that")
	}
}
