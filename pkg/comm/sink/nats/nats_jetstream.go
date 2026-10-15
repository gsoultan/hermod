// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package nats

import (
	"context"
	"fmt"

	"sync"

	"github.com/gsoultan/hermod"
	"github.com/nats-io/nats.go"
)

// NatsJetStreamSink implements the hermod.Sink interface for NATS JetStream.
type NatsJetStreamSink struct {
	nc        *nats.Conn
	js        nats.JetStreamContext
	url       string
	opts      []nats.Option
	subject   string
	formatter hermod.Formatter
	mu        sync.Mutex
}

// NewNatsJetStreamSink creates a new NATS JetStream sink.
func NewNatsJetStreamSink(url string, subject string, username, password, token string, formatter hermod.Formatter) (*NatsJetStreamSink, error) {
	opts := []nats.Option{}
	if token != "" {
		opts = append(opts, nats.Token(token))
	} else if username != "" {
		opts = append(opts, nats.UserInfo(username, password))
	}

	return &NatsJetStreamSink{
		url:       url,
		opts:      opts,
		subject:   subject,
		formatter: formatter,
	}, nil
}

func (s *NatsJetStreamSink) ensureConnected(ctx context.Context) error {
	s.mu.Lock()
	if s.nc != nil && s.nc.IsConnected() {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	nc, err := nats.Connect(s.url, s.opts...)
	if err != nil {
		return fmt.Errorf("failed to connect to NATS: %w", err)
	}

	js, err := nc.JetStream()
	if err != nil {
		nc.Close()
		return fmt.Errorf("failed to create JetStream context: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.nc != nil && s.nc.IsConnected() {
		nc.Close()
		return nil
	}
	s.nc = nc
	s.js = js
	return nil
}

// Write publishes a message to NATS JetStream.
func (s *NatsJetStreamSink) Write(ctx context.Context, msg hermod.Message) error {
	if msg == nil {
		return nil
	}
	if err := s.ensureConnected(ctx); err != nil {
		return err
	}

	s.mu.Lock()
	js := s.js
	s.mu.Unlock()

	var data []byte
	var err error

	if s.formatter != nil {
		data, err = s.formatter.Format(msg)
	} else {
		// Fallback to Payload-only if no formatter provided
		data = msg.Payload()
	}

	if err != nil {
		return fmt.Errorf("failed to format message: %w", err)
	}

	_, err = js.Publish(s.subject, data, nats.Context(ctx))
	if err != nil {
		return fmt.Errorf("failed to publish message to JetStream: %w", err)
	}

	return nil
}

// Ping checks if the NATS connection is alive.
func (s *NatsJetStreamSink) Ping(ctx context.Context) error {
	return s.ensureConnected(ctx)
}

// Close closes the NATS connection.
func (s *NatsJetStreamSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.nc != nil {
		s.nc.Close()
		s.nc = nil
	}
	return nil
}
