// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

// Package redisset is the Redis Set sink: it writes each record to a Redis key
// with SET or HSET, through pkg/infra/rediskv, which the Redis Set
// transformation writes through as well.
package redisset

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/infra/rediskv"
	"github.com/redis/go-redis/v9"
)

// RedisSetSink writes records to Redis keys.
type RedisSetSink struct {
	settings  rediskv.Settings
	formatter hermod.Formatter

	mu     sync.Mutex
	client *redis.Client
	closed bool
}

// New builds the sink from its config. A misconfiguration fails here, when the
// workflow starts, rather than on its first record.
func New(cfg map[string]string, formatter hermod.Formatter) (*RedisSetSink, error) {
	s, err := rediskv.ParseSettings(func(k string) string { return cfg[k] })
	if err != nil {
		return nil, err
	}
	return &RedisSetSink{settings: s, formatter: formatter}, nil
}

// conn returns the client, opening it on first use. go-redis connects lazily,
// so opening one costs nothing until a command is sent.
func (s *RedisSetSink) conn() (*redis.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errors.New("redis set sink is closed")
	}
	if s.client == nil {
		s.client = rediskv.NewClient(s.settings.Conn)
	}
	return s.client, nil
}

// record renders a whole record, for a write with no value: through the sink's
// format when one is set, as every other sink does, and as the data map's JSON
// otherwise -- raw payload bytes are not always JSON, and a key read back by
// another service should be.
func (s *RedisSetSink) record(msg hermod.Message) ([]byte, error) {
	if s.formatter != nil {
		return s.formatter.Format(msg)
	}
	return json.Marshal(msg.Data())
}

func (s *RedisSetSink) Write(ctx context.Context, msg hermod.Message) error {
	return s.WriteBatch(ctx, []hermod.Message{msg})
}

// WriteBatch builds every write before sending any, so a record that cannot be
// written fails the batch with nothing half-applied, then sends them all in
// one round trip.
func (s *RedisSetSink) WriteBatch(ctx context.Context, msgs []hermod.Message) error {
	ops := make([]rediskv.Op, 0, len(msgs))
	for _, msg := range msgs {
		if msg == nil {
			continue
		}
		op, err := s.settings.Build(msg, s.record)
		if err != nil {
			return err
		}
		ops = append(ops, op)
	}
	if len(ops) == 0 {
		return nil
	}
	c, err := s.conn()
	if err != nil {
		return err
	}
	return rediskv.ApplyAll(ctx, c, ops)
}

func (s *RedisSetSink) Ping(ctx context.Context) error {
	c, err := s.conn()
	if err != nil {
		return err
	}
	return c.Ping(ctx).Err()
}

func (s *RedisSetSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.client == nil {
		return nil
	}
	err := s.client.Close()
	s.client = nil
	return err
}
