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
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
	amqp "github.com/rabbitmq/amqp091-go"
)

// RabbitMQQueueSink implements the hermod.Sink interface for RabbitMQ over
// AMQP 0-9-1: straight to a queue, or to an exchange of any built-in type.
//
// Every publish is mandatory and confirmed. A message RabbitMQ could not route
// to any queue comes back to the sink, and Write reports it as failed: an
// exchange with nothing bound to it would otherwise drop the record while the
// publish still succeeded.
type RabbitMQQueueSink struct {
	url       string
	settings  QueueSinkSettings
	formatter hermod.Formatter

	// mu is held for a whole Write, so the one returned or confirmed message
	// in flight is always the one just published.
	mu      sync.Mutex
	conn    *amqp.Connection
	channel *amqp.Channel
	returns chan amqp.Return
	closed  chan *amqp.Error
}

// NewRabbitMQQueueSink creates a sink that publishes to queueName through the
// default exchange.
func NewRabbitMQQueueSink(url string, queueName string, formatter hermod.Formatter) (*RabbitMQQueueSink, error) {
	return &RabbitMQQueueSink{
		url:       url,
		settings:  QueueSinkSettings{Queue: queueName, RoutingKey: queueName},
		formatter: formatter,
	}, nil
}

// NewRabbitMQQueueSinkWithSettings creates a sink from parsed settings; see
// ParseQueueSinkSettings.
func NewRabbitMQQueueSinkWithSettings(url string, s QueueSinkSettings, formatter hermod.Formatter) (*RabbitMQQueueSink, error) {
	return &RabbitMQQueueSink{url: url, settings: s, formatter: formatter}, nil
}

// ensureConnected opens the connection and channel and sets up the exchange,
// queue and binding. s.mu must be held.
func (s *RabbitMQQueueSink) ensureConnected() error {
	if s.url == "" {
		return errors.New("rabbitmq sink url is not configured")
	}
	if !strings.HasPrefix(s.url, "amqp://") && !strings.HasPrefix(s.url, "amqps://") {
		return errors.New("rabbitmq sink url must start with 'amqp://' or 'amqps://'")
	}
	// A channel closes on its own when the broker refuses something on it,
	// such as a publish to an exchange deleted since. The connection is still
	// open then, and only checking it would leave every later write failing.
	if s.conn != nil && !s.conn.IsClosed() && s.channel != nil && !s.channel.IsClosed() {
		return nil
	}
	s.closeLocked()

	conn, err := amqp.Dial(s.url)
	if err != nil {
		return fmt.Errorf("failed to connect to RabbitMQ: %w", err)
	}
	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return fmt.Errorf("failed to open a channel: %w", err)
	}
	if err := ch.Confirm(false); err != nil {
		conn.Close()
		return fmt.Errorf("failed to turn on publisher confirms: %w", err)
	}
	returns := ch.NotifyReturn(make(chan amqp.Return, 4))
	closed := ch.NotifyClose(make(chan *amqp.Error, 1))

	if err := s.declareTopology(ch); err != nil {
		conn.Close()
		return err
	}
	s.conn, s.channel, s.returns, s.closed = conn, ch, returns, closed
	return nil
}

// declareTopology sets up what the settings name. Each step that fails closes
// the channel, so the first error is the one returned.
func (s *RabbitMQQueueSink) declareTopology(ch *amqp.Channel) error {
	st := s.settings
	if st.Exchange != "" {
		var err error
		if st.DeclareExchange {
			err = ch.ExchangeDeclare(st.Exchange, st.ExchangeType, true, false, false, false, nil)
		} else {
			err = ch.ExchangeDeclarePassive(st.Exchange, st.ExchangeType, true, false, false, false, nil)
		}
		if err != nil {
			return exchangeError(st, err)
		}
	}
	if st.Queue == "" {
		return nil
	}
	if _, err := ch.QueueDeclare(st.Queue, true, false, false, false, nil); err != nil {
		if code(err) == amqp.PreconditionFailed {
			return fmt.Errorf("rabbitmq queue %q already exists with other settings than a durable queue; use another queue name: %w", st.Queue, err)
		}
		return fmt.Errorf("failed to declare a queue: %w", err)
	}
	if st.Exchange == "" {
		return nil
	}
	key, args := st.binding()
	if err := ch.QueueBind(st.Queue, key, st.Exchange, false, args); err != nil {
		return fmt.Errorf("failed to bind queue %q to exchange %q: %w", st.Queue, st.Exchange, err)
	}
	return nil
}

// binding is the key and arguments that bind the queue to the exchange.
func (st QueueSinkSettings) binding() (string, amqp.Table) {
	switch st.ExchangeType {
	case ExchangeFanout:
		return "", nil
	case ExchangeHeaders:
		args := amqp.Table{"x-match": st.HeadersMatch}
		for _, k := range sortedKeys(st.BindingHeaders) {
			args[k] = st.BindingHeaders[k]
		}
		return "", args
	default:
		return st.BindingKey, nil
	}
}

func exchangeError(st QueueSinkSettings, err error) error {
	switch code(err) {
	case amqp.PreconditionFailed:
		return fmt.Errorf("rabbitmq exchange %q already exists with a different type or settings than a durable %s exchange; choose the type it was created with, or turn off creating the exchange: %w", st.Exchange, st.ExchangeType, err)
	case amqp.NotFound:
		return fmt.Errorf("rabbitmq exchange %q does not exist on this virtual host; create it in RabbitMQ, or turn on creating the exchange: %w", st.Exchange, err)
	case amqp.AccessRefused:
		return fmt.Errorf("rabbitmq refused exchange %q; this user may not configure it, or the name is reserved: %w", st.Exchange, err)
	}
	return fmt.Errorf("failed to declare exchange %q: %w", st.Exchange, err)
}

func code(err error) int {
	var ae *amqp.Error
	if errors.As(err, &ae) {
		return ae.Code
	}
	return 0
}

// Write publishes msg and waits for RabbitMQ to confirm it.
func (s *RabbitMQQueueSink) Write(ctx context.Context, msg hermod.Message) error {
	if msg == nil {
		return nil
	}
	pub, key, err := s.publishing(msg)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureConnected(); err != nil {
		return err
	}
	ch := s.channel
	// A return left from a publish whose wait was cancelled would otherwise be
	// read as this message's.
	for len(s.returns) > 0 {
		<-s.returns
	}

	dc, err := ch.PublishWithDeferredConfirmWithContext(ctx, s.settings.Exchange, key, true, false, pub)
	if err != nil {
		return fmt.Errorf("failed to publish message to RabbitMQ: %w", err)
	}
	acked, err := dc.WaitContext(ctx)
	if err != nil {
		return fmt.Errorf("rabbitmq did not confirm the message: %w", err)
	}
	if !acked {
		if ch.IsClosed() {
			return fmt.Errorf("rabbitmq closed the channel before confirming the message: %v", s.closeReason())
		}
		return errors.New("rabbitmq refused the message (negative acknowledgement)")
	}
	// RabbitMQ sends a mandatory message's return before its ack, so it is
	// already here when the message was not routed.
	select {
	case r := <-s.returns:
		return s.unroutable(r)
	default:
	}
	return nil
}

func (s *RabbitMQQueueSink) unroutable(r amqp.Return) error {
	if s.settings.Exchange == "" {
		return fmt.Errorf("rabbitmq: no queue received the message; queue %q does not exist (%s)", r.RoutingKey, r.ReplyText)
	}
	return fmt.Errorf("rabbitmq: no queue received the message; nothing is bound to %s exchange %q for routing key %q (%s)",
		s.settings.ExchangeType, s.settings.Exchange, r.RoutingKey, r.ReplyText)
}

func (s *RabbitMQQueueSink) closeReason() string {
	select {
	case e := <-s.closed:
		if e != nil {
			return e.Error()
		}
	default:
	}
	return "no reason given"
}

// publishing renders msg into the message to send and its routing key.
func (s *RabbitMQQueueSink) publishing(msg hermod.Message) (amqp.Publishing, string, error) {
	st := s.settings
	var templates []string
	if hasTemplate(st.RoutingKey) {
		templates = append(templates, st.RoutingKey)
	}
	for _, v := range st.Headers {
		if hasTemplate(v) {
			templates = append(templates, v)
		}
	}
	if len(templates) > 0 {
		if empty := evaluator.EmptyTokens(msg, templates...); len(empty) > 0 {
			return amqp.Publishing{}, "", fmt.Errorf("rabbitmq sink: these tokens had no value, so nothing was published: %s", strings.Join(empty, ", "))
		}
	}

	var data []byte
	var err error
	if s.formatter != nil {
		data, err = s.formatter.Format(msg)
	} else {
		// Fallback to Payload-only if no formatter provided
		data = msg.Payload()
	}
	if err != nil {
		return amqp.Publishing{}, "", fmt.Errorf("failed to format message: %w", err)
	}

	pub := amqp.Publishing{ContentType: "application/json", DeliveryMode: amqp.Persistent, Body: data}
	if len(st.Headers) > 0 {
		pub.Headers = amqp.Table{}
		for k, v := range st.Headers {
			pub.Headers[k] = evaluator.ResolveTemplateMsg(v, msg)
		}
	}
	key := st.RoutingKey
	if hasTemplate(key) {
		key = evaluator.ResolveTemplateMsg(key, msg)
	}
	return pub, key, nil
}

// Ping connects and sets up the exchange, queue and binding, so a test of the
// connection also proves the topology is what the settings say.
func (s *RabbitMQQueueSink) Ping(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ensureConnected()
}

// Close closes the RabbitMQ channel and connection.
func (s *RabbitMQQueueSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeLocked()
	return nil
}

func (s *RabbitMQQueueSink) closeLocked() {
	if s.channel != nil {
		s.channel.Close()
		s.channel = nil
	}
	if s.conn != nil {
		s.conn.Close()
		s.conn = nil
	}
}
