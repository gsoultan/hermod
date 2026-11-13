// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package rediskv

import (
	"errors"
	"net"
	"strings"
	"syscall"
	"testing"

	"github.com/alicebob/miniredis/v2"
)

func get(cfg map[string]string) func(string) string {
	return func(k string) string { return cfg[k] }
}

func TestParseConnNeedsAnAddress(t *testing.T) {
	// An empty address used to reach go-redis, which then dialled
	// localhost:6379 and reported "connection refused" for a server the user
	// never named.
	_, err := ParseConn(get(map[string]string{"addr": "  "}))
	if err == nil || !strings.Contains(err.Error(), "address is required") {
		t.Fatalf("ParseConn(empty addr) = %v, want an 'address is required' error", err)
	}
}

func TestParseConnAddsTheDefaultPort(t *testing.T) {
	c, err := ParseConn(get(map[string]string{"addr": "redis.internal"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Addr != "redis.internal:6379" {
		t.Fatalf("Addr = %q, want redis.internal:6379", c.Addr)
	}
	c, err = ParseConn(get(map[string]string{"addr": "::1"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Addr != "[::1]:6379" {
		t.Fatalf("Addr = %q, want [::1]:6379", c.Addr)
	}
}

func TestParseConnReadsARedisURL(t *testing.T) {
	c, err := ParseConn(get(map[string]string{"addr": "rediss://app:s3cret@cache.example.com:6380/2"}))
	if err != nil {
		t.Fatal(err)
	}
	want := Conn{Addr: "cache.example.com:6380", Username: "app", Password: "s3cret", DB: 2, TLS: true}
	if c != want {
		t.Fatalf("ParseConn(url) = %+v, want %+v", c, want)
	}
}

func TestParseConnLetsFieldsOverrideTheURL(t *testing.T) {
	c, err := ParseConn(get(map[string]string{
		"addr": "redis://cache:6379/1", "password": "fromfield", "db": "3", "tls": "true", "username": "u",
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := Conn{Addr: "cache:6379", Username: "u", Password: "fromfield", DB: 3, TLS: true}
	if c != want {
		t.Fatalf("ParseConn = %+v, want %+v", c, want)
	}
}

func TestParseConnRefusesABadDB(t *testing.T) {
	if _, err := ParseConn(get(map[string]string{"addr": "h:1", "db": "x"})); err == nil {
		t.Fatal("want an error for db=x")
	}
}

// closedAddr returns a loopback address with nothing listening on it.
func closedAddr(t *testing.T) string {
	t.Helper()
	l, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

func TestExplainConnectionRefusedSaysWhatToCheck(t *testing.T) {
	conn := Conn{Addr: closedAddr(t)}
	c := NewClient(conn)
	defer c.Close()
	err := ExplainConnError(conn, c.Ping(t.Context()).Err())
	if err == nil {
		t.Fatal("want an error")
	}
	if !errors.Is(err, syscall.ECONNREFUSED) {
		t.Fatalf("the cause is lost: %v", err)
	}
	msg := err.Error()
	for _, want := range []string{conn.Addr, "connection refused", "redis-cli", "bind", "protected-mode", "port", "firewall", "Docker"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message lacks %q:\n%s", want, msg)
		}
	}
}

func TestExplainWrongPasswordPointsAtRequirepassAndACL(t *testing.T) {
	mr := miniredis.RunT(t)
	mr.RequireAuth("right")
	conn := Conn{Addr: mr.Addr(), Password: "wrong"}
	c := NewClient(conn)
	defer c.Close()
	msg := ExplainConnError(conn, c.Ping(t.Context()).Err()).Error()
	for _, want := range []string{"password", "requirepass", "ACL"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message lacks %q:\n%s", want, msg)
		}
	}
}

func TestExplainMissingPasswordPointsAtRequirepass(t *testing.T) {
	mr := miniredis.RunT(t)
	mr.RequireAuth("right")
	conn := Conn{Addr: mr.Addr()}
	c := NewClient(conn)
	defer c.Close()
	msg := ExplainConnError(conn, c.Ping(t.Context()).Err()).Error()
	if !strings.Contains(msg, "requirepass") {
		t.Errorf("message lacks requirepass:\n%s", msg)
	}
}

func TestExplainKnowsRedisServerReplies(t *testing.T) {
	conn := Conn{Addr: "cache:6379"}
	cases := map[string]string{
		"DENIED Redis is running in protected mode because protected mode is enabled": "protected-mode",
		"ERR max number of clients reached":                                           "maxclients",
		"NOPERM User app has no permissions to run the 'xadd' command":                "ACL SETUSER",
		"tls: first record does not look like a TLS handshake":                        "TLS",
		"EOF": "TLS",
	}
	for in, want := range cases {
		msg := ExplainConnError(conn, errors.New(in)).Error()
		if !strings.Contains(msg, want) {
			t.Errorf("ExplainConnError(%q) lacks %q:\n%s", in, want, msg)
		}
	}
}

func TestExplainLeavesOtherErrorsAndNilAlone(t *testing.T) {
	if ExplainConnError(Conn{}, nil) != nil {
		t.Fatal("nil must stay nil")
	}
	in := errors.New("WRONGTYPE Operation against a key holding the wrong kind of value")
	var ce *ConnError
	if got := ExplainConnError(Conn{Addr: "h:1"}, in); errors.As(got, &ce) || got.Error() != in.Error() {
		t.Fatalf("an unrelated error was rewritten: %v", got)
	}
}

func TestParseConnKeepsTheURLPasswordOutOfItsError(t *testing.T) {
	// A config error becomes the workflow status and the "Workflow Error"
	// alert, so it must not carry the password from a redis:// URL.
	for _, raw := range []string{"redis://app:hunter2@cache:6379/%zz", "redis://app:hunter2@cache:6379/notadb"} {
		_, err := ParseConn(get(map[string]string{"addr": raw}))
		if err == nil {
			t.Fatalf("ParseConn(%q) = nil, want an error", raw)
		}
		if strings.Contains(err.Error(), "hunter2") {
			t.Fatalf("the password leaked: %v", err)
		}
	}
}
