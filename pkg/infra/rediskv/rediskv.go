// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

// Package rediskv writes a record to a Redis key: SET for a value, HSET for a
// hash. The Redis Set sink and the Redis Set transformation both write through
// it, so the two read one config the same way and write the same bytes.
package rediskv

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
	"github.com/redis/go-redis/v9"
)

// Conn is where a write goes.
type Conn struct {
	Addr     string
	Username string
	Password string
	DB       int
	TLS      bool
}

// NewClient opens a client for conn. The caller owns it and closes it.
func NewClient(conn Conn) *redis.Client {
	opts := &redis.Options{
		Addr:     conn.Addr,
		Username: conn.Username,
		Password: conn.Password,
		DB:       conn.DB,
		// Without this go-redis applies its own read and write timeouts and
		// ignores the context's deadline, so a caller's deadline would not
		// bound a write to a server that has stopped answering.
		ContextTimeoutEnabled: true,
	}
	if conn.TLS {
		host, _, err := net.SplitHostPort(conn.Addr)
		if err != nil {
			host = conn.Addr
		}
		opts.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host}
	}
	return redis.NewClient(opts)
}

// Pool holds one client per connection for the life of its owner, so a
// transformation that writes on every record does not open a pool per record.
// The zero value is ready to use.
type Pool struct {
	mu sync.Mutex
	m  map[Conn]*redis.Client
}

// Get returns conn's client, opening it on first use.
func (p *Pool) Get(conn Conn) *redis.Client {
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.m[conn]; ok {
		return c
	}
	if p.m == nil {
		p.m = map[Conn]*redis.Client{}
	}
	c := NewClient(conn)
	p.m[conn] = c
	return c
}

// Len is the number of clients open.
func (p *Pool) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.m)
}

// Close closes every client.
func (p *Pool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for k, c := range p.m {
		_ = c.Close()
		delete(p.m, k)
	}
}

// Settings is a node's write, before any record is applied to it. Key, Field
// and Value are templates over the record.
type Settings struct {
	Conn
	// Command is "set" or "hset".
	Command string
	Key     string
	// Field is the hash field HSET writes. Empty means every top-level field
	// of the value, which then has to be an object.
	Field string
	// Value is what is written. Empty means the whole record.
	Value string
	// TTL expires the key; zero leaves it without expiry.
	TTL time.Duration
	// OnlyIfAbsent writes a SET only when the key does not exist yet (NX).
	OnlyIfAbsent bool
}

// ParseSettings reads a node's config through get, which returns "" for a key
// that is not set. Every failure is a misconfiguration, reported before any
// record is written.
func ParseSettings(get func(key string) string) (Settings, error) {
	s := Settings{
		Conn: Conn{
			Addr:     strings.TrimSpace(get("addr")),
			Username: get("username"),
			Password: get("password"),
			TLS:      parseBool(get("tls")),
		},
		Command:      strings.ToLower(strings.TrimSpace(get("command"))),
		Key:          get("key"),
		Field:        get("field"),
		Value:        get("value"),
		OnlyIfAbsent: parseBool(get("onlyIfAbsent")),
	}
	if s.Command == "" {
		s.Command = "set"
	}
	if s.Addr == "" {
		return Settings{}, errors.New("redis set: addr is required")
	}
	if strings.TrimSpace(s.Key) == "" {
		return Settings{}, errors.New("redis set: key is required")
	}
	if s.Command != "set" && s.Command != "hset" {
		return Settings{}, fmt.Errorf("redis set: command %q is not one of set, hset", s.Command)
	}
	// HSETNX exists for one field only, and with an expiry it could not be
	// told apart from a write that refreshed the key. Refusing it is clearer
	// than an only-if-absent that sometimes is not.
	if s.OnlyIfAbsent && s.Command == "hset" {
		return Settings{}, errors.New("redis set: only-if-absent applies to set, not hset")
	}
	if db := strings.TrimSpace(get("db")); db != "" {
		n, err := strconv.Atoi(db)
		if err != nil || n < 0 {
			return Settings{}, fmt.Errorf("redis set: db %q is not a database number", db)
		}
		s.DB = n
	}
	if ttl := strings.TrimSpace(get("ttl")); ttl != "" {
		d, err := time.ParseDuration(ttl)
		if err != nil {
			return Settings{}, fmt.Errorf("redis set: ttl %q is not a duration (it needs a unit, e.g. %q or %q)", ttl, "30s", "1h")
		}
		if d < 0 {
			return Settings{}, fmt.Errorf("redis set: ttl %q is negative", ttl)
		}
		s.TTL = d
	}
	return s, nil
}

func parseBool(s string) bool {
	return strings.EqualFold(strings.TrimSpace(s), "true")
}

// Op is one write, resolved against one record.
type Op struct {
	Command      string
	Key          string
	Field        string
	Value        []byte
	Fields       map[string]any
	TTL          time.Duration
	OnlyIfAbsent bool
}

// Build resolves s against msg. record renders the whole record, used for a
// SET when no value is given: the sink renders it with its formatter, the
// transformation as its data map.
//
// A key, field or value whose token has no value on the record is refused. A
// key such as "user:" is still a valid Redis key, and writing it would let
// every record without a user overwrite the one before.
func (s Settings) Build(msg hermod.Message, record func(hermod.Message) ([]byte, error)) (Op, error) {
	if empty := evaluator.EmptyTokens(msg, s.Key, s.Field, s.Value); len(empty) > 0 {
		return Op{}, fmt.Errorf("redis set: these tokens had no value, so nothing was written: %s", strings.Join(empty, ", "))
	}
	op := Op{
		Command:      s.Command,
		Key:          evaluator.ResolveTemplateMsg(s.Key, msg),
		TTL:          s.TTL,
		OnlyIfAbsent: s.OnlyIfAbsent,
	}

	if s.Command == "hset" && s.Field == "" {
		fields, err := s.hashFields(msg)
		if err != nil {
			return Op{}, err
		}
		op.Fields = fields
		return op, nil
	}

	if s.Command == "hset" {
		op.Field = evaluator.ResolveTemplateMsg(s.Field, msg)
	}
	if s.Value == "" {
		b, err := record(msg)
		if err != nil {
			return Op{}, fmt.Errorf("redis set: rendering the record: %w", err)
		}
		op.Value = b
		return op, nil
	}
	op.Value = []byte(resolveValue(s.Value, msg))
	return op, nil
}

// resolveValue renders a value template. A template that is JSON is resolved
// inside the JSON, so an object field stays an object and a value holding a
// quote stays escaped; any other template renders as text, which writes an
// object field as its JSON.
func resolveValue(temp string, msg hermod.Message) string {
	if v, ok := evaluator.ResolveJSONTemplateMsg(temp, msg); ok {
		return v
	}
	return evaluator.ResolveTemplateMsg(temp, msg)
}

// hashFields is the object an HSET with no field writes: the record's data
// map, or the value template, which then has to render an object.
func (s Settings) hashFields(msg hermod.Message) (map[string]any, error) {
	var obj map[string]any
	if s.Value == "" {
		obj = msg.Data()
	} else {
		dec := json.NewDecoder(strings.NewReader(resolveValue(s.Value, msg)))
		dec.UseNumber()
		if err := dec.Decode(&obj); err != nil {
			return nil, errors.New("redis set: hset with no field writes an object's fields, and the value is not a JSON object")
		}
	}
	if len(obj) == 0 {
		return nil, errors.New("redis set: hset with no field has no fields to write")
	}
	fields := make(map[string]any, len(obj))
	for k, v := range obj {
		fields[k] = fieldText(v)
	}
	return fields, nil
}

// fieldText is a hash field's value as Redis stores it: text as itself, a
// number as its digits, anything else as JSON.
func fieldText(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case json.Number:
		return t.String()
	case []byte:
		return string(t)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(bytes.TrimSpace(b))
}

// queue adds op's commands to p.
func queue(ctx context.Context, p redis.Cmdable, op Op) {
	switch {
	case op.Command == "set":
		args := redis.SetArgs{TTL: op.TTL}
		if op.OnlyIfAbsent {
			args.Mode = "NX"
		}
		p.SetArgs(ctx, op.Key, op.Value, args)
		return
	case op.Field != "":
		p.HSet(ctx, op.Key, op.Field, op.Value)
	default:
		p.HSet(ctx, op.Key, op.Fields)
	}
	if op.TTL > 0 {
		p.Expire(ctx, op.Key, op.TTL)
	}
}

// Apply writes op. The hash and its expiry go in one MULTI, so a key is never
// left without the expiry it was written with.
func Apply(ctx context.Context, c *redis.Client, op Op) error {
	return ApplyAll(ctx, c, []Op{op})
}

// ApplyAll writes every op in one round trip, each op's commands in order and
// all of them in one MULTI. It returns every write that failed.
func ApplyAll(ctx context.Context, c *redis.Client, ops []Op) error {
	if len(ops) == 0 {
		return nil
	}
	cmds, err := c.TxPipelined(ctx, func(p redis.Pipeliner) error {
		for _, op := range ops {
			queue(ctx, p, op)
		}
		return nil
	})
	var errs []error
	for _, cmd := range cmds {
		// A SET NX that found the key answers nil: written by someone else
		// first, which is what only-if-absent asked for.
		if e := cmd.Err(); e != nil && !errors.Is(e, redis.Nil) {
			errs = append(errs, fmt.Errorf("redis set: %s: %w", cmd.Name(), e))
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	if err != nil && !errors.Is(err, redis.Nil) {
		return fmt.Errorf("redis set: %w", err)
	}
	return nil
}
