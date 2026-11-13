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
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/infra/rediskv"
	goredis "github.com/redis/go-redis/v9"
)

func deadAddr(t *testing.T) string {
	t.Helper()
	l, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

func TestNewRefusesAnEmptyAddress(t *testing.T) {
	// Before, an empty address reached go-redis, which dialled localhost:6379.
	_, err := New(map[string]string{"stream": "s"}, nil)
	if err == nil || !strings.Contains(err.Error(), "address is required") {
		t.Fatalf("New(no addr) = %v, want an 'address is required' error", err)
	}
}

func TestNewRefusesAnEmptyStream(t *testing.T) {
	_, err := New(map[string]string{"addr": "h:1"}, nil)
	if err == nil || !strings.Contains(err.Error(), "stream is required") {
		t.Fatalf("New(no stream) = %v, want a 'stream is required' error", err)
	}
}

func TestPingExplainsAConnectionRefused(t *testing.T) {
	snk, err := New(map[string]string{"addr": deadAddr(t), "stream": "s"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer snk.Close()
	err = snk.Ping(t.Context())
	var ce *rediskv.ConnError
	if !errors.As(err, &ce) {
		t.Fatalf("Ping = %v, want a *rediskv.ConnError", err)
	}
	if !strings.Contains(err.Error(), "What to check:") {
		t.Fatalf("no guidance in:\n%s", err)
	}
}

func TestWriteExplainsAConnectionRefused(t *testing.T) {
	snk, err := New(map[string]string{"addr": deadAddr(t), "stream": "s"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer snk.Close()
	m := message.AcquireMessage()
	defer m.Release()
	m.SetPayload([]byte(`{"a":1}`))
	var ce *rediskv.ConnError
	if err := snk.Write(t.Context(), m); !errors.As(err, &ce) {
		t.Fatalf("Write = %v, want a *rediskv.ConnError", err)
	}
}

func TestWriteUsesTheConfiguredDatabaseAndURL(t *testing.T) {
	mr := miniredis.RunT(t)
	mr.RequireUserAuth("app", "pw")
	snk, err := New(map[string]string{"addr": "redis://app:pw@" + mr.Addr() + "/2", "stream": "events"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer snk.Close()
	m := message.AcquireMessage()
	defer m.Release()
	m.SetPayload([]byte(`{"a":1}`))
	if err := snk.Write(t.Context(), m); err != nil {
		t.Fatalf("Write: %v", err)
	}
	c := goredis.NewClient(&goredis.Options{Addr: mr.Addr(), Username: "app", Password: "pw", DB: 2})
	defer c.Close()
	n, err := c.XLen(t.Context(), "events").Result()
	if err != nil || n != 1 {
		t.Fatalf("XLEN events in db 2 = %d, %v; want 1", n, err)
	}
}
