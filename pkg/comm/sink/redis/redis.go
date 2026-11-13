// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package redis

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/infra/rediskv"
	"github.com/redis/go-redis/v9"
)

// RedisSink implements the hermod.Sink interface for Redis (using Streams).
type RedisSink struct {
	conn      rediskv.Conn
	stream    string
	formatter hermod.Formatter
	client    *redis.Client
	mu        sync.Mutex
	// idempotency reporting (last write outcome)
	lastDedup    bool
	lastConflict bool
}

// New builds the sink from its config: addr (host:port, a bare host, or a
// redis:// or rediss:// URL), username, password, db, tls and stream. A
// misconfiguration fails here, when the workflow starts, not on its first record.
func New(cfg map[string]string, formatter hermod.Formatter) (*RedisSink, error) {
	conn, err := rediskv.ParseConn(func(k string) string { return cfg[k] })
	if err != nil {
		return nil, fmt.Errorf("redis stream sink: %w", err)
	}
	stream := strings.TrimSpace(cfg["stream"])
	if stream == "" {
		return nil, errors.New("redis stream sink: the stream is required")
	}
	return &RedisSink{conn: conn, stream: stream, formatter: formatter}, nil
}

// NewRedisSink builds a sink from an address, password and stream.
func NewRedisSink(addr string, password string, stream string, formatter hermod.Formatter) (*RedisSink, error) {
	return New(map[string]string{"addr": addr, "password": password, "stream": stream}, formatter)
}

func (s *RedisSink) init(ctx context.Context) error {
	s.mu.Lock()
	if s.client != nil {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	cl := rediskv.NewClient(s.conn)

	if err := cl.Ping(ctx).Err(); err != nil {
		cl.Close()
		return rediskv.ExplainConnError(s.conn, err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil {
		cl.Close()
		return nil
	}
	s.client = cl
	return nil
}

func (s *RedisSink) Write(ctx context.Context, msg hermod.Message) error {
	if msg == nil {
		return nil
	}

	s.mu.Lock()
	cl := s.client
	s.mu.Unlock()
	if cl == nil {
		if err := s.init(ctx); err != nil {
			return err
		}
		s.mu.Lock()
		cl = s.client
		s.mu.Unlock()
	}

	// reset last outcome
	s.mu.Lock()
	s.lastDedup = false
	s.lastConflict = false
	s.mu.Unlock()

	// Idempotency via SETNX with TTL: if message has an ID, use it to dedupe
	if id := msg.ID(); id != "" {
		ttl := 24 * time.Hour
		if v := os.Getenv("HERMOD_IDEMPOTENCY_TTL"); v != "" {
			if d, err := time.ParseDuration(v); err == nil && d > 0 {
				ttl = d
			}
		}
		ns := os.Getenv("HERMOD_IDEMPOTENCY_NAMESPACE")
		if ns == "" {
			ns = "hermod:idemp"
		}
		key := fmt.Sprintf("%s:%s:%s", ns, s.stream, id)
		ok, err := cl.SetNX(ctx, key, "1", ttl).Result()
		if err != nil {
			return fmt.Errorf("redis idempotency setnx error: %w", rediskv.ExplainConnError(s.conn, err))
		}
		if !ok {
			// Duplicate message; treat as handled without re-publishing
			s.mu.Lock()
			s.lastDedup = true
			s.mu.Unlock()
			return nil
		}
	}

	var data []byte
	var err error

	if s.formatter != nil {
		data, err = s.formatter.Format(msg)
	} else {
		data = msg.Payload()
	}

	if err != nil {
		return fmt.Errorf("failed to format message: %w", err)
	}

	err = cl.XAdd(ctx, &redis.XAddArgs{
		Stream: s.stream,
		Values: map[string]any{"data": data},
	}).Err()

	if err != nil {
		return fmt.Errorf("failed to publish to redis stream: %w", rediskv.ExplainConnError(s.conn, err))
	}

	return nil
}

// LastWriteIdempotent reports whether the last Write call resulted in a dedup skip
// or a conflict. Redis sink only reports dedup (conflicts are not applicable).
func (s *RedisSink) LastWriteIdempotent() (bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastDedup, s.lastConflict
}

func (s *RedisSink) Ping(ctx context.Context) error {
	s.mu.Lock()
	cl := s.client
	s.mu.Unlock()
	if cl == nil {
		if err := s.init(ctx); err != nil {
			return err
		}
		s.mu.Lock()
		cl = s.client
		s.mu.Unlock()
	}
	return rediskv.ExplainConnError(s.conn, cl.Ping(ctx).Err())
}

func (s *RedisSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil {
		err := s.client.Close()
		s.client = nil
		return err
	}
	return nil
}
