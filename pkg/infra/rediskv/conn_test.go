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
	"io"
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

// hangUpAddr starts a server that accepts each connection and closes it
// without a reply, as a TLS-only Redis port does with a plain client, and as
// a Docker published port does when its container is down.
func hangUpAddr(t *testing.T) string {
	t.Helper()
	l, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	return l.Addr().String()
}

func TestExplainHangUpWithTLSOffDoesNotBlameTLSSettings(t *testing.T) {
	conn := Conn{Addr: hangUpAddr(t)}
	c := NewClient(conn)
	defer c.Close()
	msg := ExplainConnError(conn, c.Ping(t.Context()).Err()).Error()
	for _, want := range []string{"closed the connection", "Use TLS is off", "tls-port", "Docker", "redis-cli"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message lacks %q:\n%s", want, msg)
		}
	}
	for _, unwanted := range []string{"certificate", "TLS port when TLS is on"} {
		if strings.Contains(msg, unwanted) {
			t.Errorf("message talks about %q although TLS is off:\n%s", unwanted, msg)
		}
	}
}

func TestExplainHangUpWithTLSOnPointsAtAPlainServer(t *testing.T) {
	conn := Conn{Addr: "cache:6379", TLS: true}
	msg := ExplainConnError(conn, errors.New("EOF")).Error()
	for _, want := range []string{"closed the connection", "TLS is on", "rediss://", "Use TLS off"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message lacks %q:\n%s", want, msg)
		}
	}
}

func TestExplainConnectionResetWithTLSOff(t *testing.T) {
	conn := Conn{Addr: "cache:6379"}
	msg := ExplainConnError(conn, &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}).Error()
	if !strings.Contains(msg, "Use TLS is off") || strings.Contains(msg, "certificate") {
		t.Errorf("a reset with TLS off is explained as a TLS setup problem:\n%s", msg)
	}
}

func TestExplainCertificateErrorsKeepTheTLSChecks(t *testing.T) {
	conn := Conn{Addr: "cache:6380", TLS: true}
	msg := ExplainConnError(conn, errors.New("tls: failed to verify certificate: x509: certificate is valid for a, not cache")).Error()
	if !strings.Contains(msg, "certificate is valid for the host name") {
		t.Errorf("message lacks the certificate check:\n%s", msg)
	}
}

// refusingAddr starts a server that answers each connection the way Redis
// refuses a client it will not serve: it writes reply before any request,
// then hangs up.
func refusingAddr(t *testing.T, reply string) string {
	t.Helper()
	l, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			_, _ = c.Write([]byte(reply + "\r\n"))
			_ = c.Close()
		}
	}()
	return l.Addr().String()
}

// go-redis reads Redis's refusal as HELLO's reply, takes it for a server
// without HELLO, and reports the EOF of its next command instead.
func TestPingReportsProtectedModeInsteadOfEOF(t *testing.T) {
	denied := "-DENIED Redis is running in protected mode because protected mode is enabled and no password is set for the default user."
	for _, conn := range []Conn{
		{Addr: refusingAddr(t, denied)},
		{Addr: refusingAddr(t, denied), Username: "app", Password: "s3cret"},
	} {
		c := NewClient(conn)
		err := ExplainConnError(conn, c.Ping(t.Context()).Err())
		_ = c.Close()
		msg := err.Error()
		for _, want := range []string{"DENIED Redis is running in protected mode", "protected-mode", "requirepass"} {
			if !strings.Contains(msg, want) {
				t.Errorf("username %q: message lacks %q:\n%s", conn.Username, want, msg)
			}
		}
		if strings.Contains(msg, "TLS") {
			t.Errorf("username %q: message blames TLS:\n%s", conn.Username, msg)
		}
	}
}

func TestPingReportsMaxClientsInsteadOfEOF(t *testing.T) {
	conn := Conn{Addr: refusingAddr(t, "-ERR max number of clients reached")}
	c := NewClient(conn)
	defer c.Close()
	msg := ExplainConnError(conn, c.Ping(t.Context()).Err()).Error()
	if !strings.Contains(msg, "maxclients") {
		t.Errorf("message lacks maxclients:\n%s", msg)
	}
}

func TestARefusalIsForgottenOnceACommandSucceeds(t *testing.T) {
	g := &greeting{}
	g.refusal.Store(new(string))
	*g.refusal.Load() = "DENIED old"
	if err := g.explain(nil); err != nil {
		t.Fatalf("explain(nil) = %v", err)
	}
	var re *RefusedError
	if got := g.explain(io.EOF); errors.As(got, &re) {
		t.Fatalf("a refusal outlived a successful command: %v", got)
	}
}

func TestExplainBrokenPipeAsAHangUp(t *testing.T) {
	conn := Conn{Addr: "cache:6379"}
	msg := ExplainConnError(conn, &net.OpError{Op: "write", Net: "tcp", Err: syscall.EPIPE}).Error()
	if !strings.Contains(msg, "closed the connection") {
		t.Errorf("a broken pipe gets no guidance:\n%s", msg)
	}
}

func TestPingOnAnOldRedisIsNotARefusal(t *testing.T) {
	g := &greeting{}
	c := &greetingConn{Conn: fakeConn{data: "-ERR unknown command 'HELLO'\r\n"}, g: g}
	_, _ = c.Read(make([]byte, 64))
	var re *RefusedError
	if got := g.explain(io.EOF); errors.As(got, &re) {
		t.Fatalf("an unknown-command reply was kept as a refusal: %v", got)
	}
}

type fakeConn struct {
	net.Conn
	data string
}

func (f fakeConn) Read(p []byte) (int, error) { return copy(p, f.data), nil }
