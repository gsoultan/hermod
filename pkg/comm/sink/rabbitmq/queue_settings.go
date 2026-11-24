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
	"errors"
	"fmt"
	"sort"
	"strings"
)

// The exchange types RabbitMQ ships with. Plugin types such as
// x-consistent-hash are not offered: a broker without the plugin refuses them,
// and the form could not say how to route to them.
const (
	ExchangeDirect  = "direct"
	ExchangeFanout  = "fanout"
	ExchangeTopic   = "topic"
	ExchangeHeaders = "headers"
)

// QueueSinkSettings is where the rabbitmq_queue sink sends a message and
// what it sets up on the broker first.
//
// With no Exchange it publishes through the default exchange, which routes
// by queue name: the sink's behaviour before exchanges existed. With an
// Exchange, Queue is optional. When set, the sink creates it and binds it to
// the exchange, so a message always has somewhere to go.
type QueueSinkSettings struct {
	Queue        string
	Exchange     string
	ExchangeType string
	// DeclareExchange creates the exchange when it is missing. When false the
	// exchange has to exist already; the sink only checks that it does.
	DeclareExchange bool
	// RoutingKey is a template over the record (direct and topic).
	RoutingKey string
	// BindingKey binds Queue to a direct or topic exchange.
	BindingKey string
	// Headers are set on every message; their values are templates. A
	// headers exchange routes by them.
	Headers map[string]string
	// BindingHeaders and HeadersMatch bind Queue to a headers exchange.
	BindingHeaders map[string]string
	HeadersMatch   string
}

// ParseQueueSinkSettings reads a sink's config through get, which returns ""
// for a key that is not set. Every error is a misconfiguration, reported
// before anything is sent. Fields that belong to another exchange type are
// ignored: they are what is left after switching type in the form.
func ParseQueueSinkSettings(get func(key string) string) (QueueSinkSettings, error) {
	s := QueueSinkSettings{
		Queue:    strings.TrimSpace(get("queue_name")),
		Exchange: strings.TrimSpace(get("exchange")),
	}
	if s.Exchange == "" {
		if s.Queue == "" {
			return QueueSinkSettings{}, errors.New("rabbitmq sink: set a queue name or an exchange to publish to")
		}
		// The default exchange delivers to the queue named by the routing key.
		s.RoutingKey = s.Queue
		return s, nil
	}
	if s.Exchange == "amq.default" {
		return QueueSinkSettings{}, errors.New("rabbitmq sink: amq.default is the default exchange; to use it, leave the exchange empty and set a queue name")
	}

	s.ExchangeType = strings.ToLower(strings.TrimSpace(get("exchange_type")))
	if s.ExchangeType == "" {
		s.ExchangeType = ExchangeDirect
	}
	// The amq.* exchanges are built in, and RabbitMQ refuses a client that
	// declares a name with that prefix.
	s.DeclareExchange = !strings.EqualFold(strings.TrimSpace(get("exchange_declare")), "false") &&
		!strings.HasPrefix(s.Exchange, "amq.")

	switch s.ExchangeType {
	case ExchangeDirect, ExchangeTopic:
		if err := s.parseRoutingKey(get); err != nil {
			return QueueSinkSettings{}, err
		}
	case ExchangeFanout:
		// A fanout exchange copies every message to every bound queue and
		// ignores routing keys.
	case ExchangeHeaders:
		if err := s.parseHeaders(get); err != nil {
			return QueueSinkSettings{}, err
		}
	default:
		return QueueSinkSettings{}, fmt.Errorf("rabbitmq sink: exchange type %q is not one of direct, fanout, topic, headers", s.ExchangeType)
	}
	return s, nil
}

func (s *QueueSinkSettings) parseRoutingKey(get func(string) string) error {
	s.RoutingKey = strings.TrimSpace(get("routing_key"))
	if s.RoutingKey == "" {
		return fmt.Errorf("rabbitmq sink: a %s exchange routes by routing key; set one, such as orders.created", s.ExchangeType)
	}
	if s.ExchangeType == ExchangeTopic && strings.ContainsAny(stripTemplates(s.RoutingKey), "*#") {
		return errors.New("rabbitmq sink: * and # match words only in a binding key; a routing key is sent as written, so put the pattern in the binding key")
	}
	if s.Queue == "" {
		return nil
	}
	s.BindingKey = strings.TrimSpace(get("binding_key"))
	if s.BindingKey != "" {
		return nil
	}
	if hasTemplate(s.RoutingKey) {
		return errors.New("rabbitmq sink: the routing key changes per record, so the queue needs a binding key that says which of them it receives")
	}
	s.BindingKey = s.RoutingKey
	return nil
}

func (s *QueueSinkSettings) parseHeaders(get func(string) string) error {
	var err error
	if s.Headers, err = parseHeaderPairs(get("headers")); err != nil {
		return err
	}
	if len(s.Headers) == 0 {
		return errors.New("rabbitmq sink: a headers exchange routes by message headers; set at least one header, such as region=eu")
	}
	s.HeadersMatch = strings.ToLower(strings.TrimSpace(get("headers_match")))
	if s.HeadersMatch == "" {
		s.HeadersMatch = "all"
	}
	if s.HeadersMatch != "all" && s.HeadersMatch != "any" {
		return fmt.Errorf("rabbitmq sink: header match %q must be all or any", s.HeadersMatch)
	}
	if s.Queue == "" {
		return nil
	}
	if s.BindingHeaders, err = parseHeaderPairs(get("binding_headers")); err != nil {
		return err
	}
	if len(s.BindingHeaders) > 0 {
		return nil
	}
	for k, v := range s.Headers {
		if hasTemplate(v) {
			return fmt.Errorf("rabbitmq sink: header %s changes per record, so the queue needs binding headers that say which records it receives", k)
		}
	}
	s.BindingHeaders = s.Headers
	return nil
}

// parseHeaderPairs reads key=value pairs, one per line or comma separated.
func parseHeaderPairs(raw string) (map[string]string, error) {
	out := map[string]string{}
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool { return r == '\n' || r == ',' }) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if !ok || k == "" {
			return nil, fmt.Errorf("rabbitmq sink: header %q must be written key=value", part)
		}
		if strings.EqualFold(k, "x-match") {
			return nil, errors.New("rabbitmq sink: x-match is set by the header match option, not as a header")
		}
		out[k] = v
	}
	return out, nil
}

func hasTemplate(s string) bool { return strings.Contains(s, "{{") }

// stripTemplates removes {{...}} tokens, so a check on the literal text of a
// routing key does not trip on characters inside a token.
func stripTemplates(s string) string {
	var b strings.Builder
	for {
		i := strings.Index(s, "{{")
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i])
		j := strings.Index(s[i:], "}}")
		if j < 0 {
			return b.String()
		}
		s = s[i+j+2:]
	}
}

// sortedKeys is m's keys in order, so errors and tests are stable.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
