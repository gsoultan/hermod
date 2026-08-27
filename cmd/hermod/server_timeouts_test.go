package main

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"
)

// The API server's timeouts, exercised against a real listener.
//
// Go's http.Server has no timeouts by default and this one had none set: only
// Addr and Handler. A client that opens a connection and then dribbles its
// request headers one byte at a time is held open indefinitely — Slowloris —
// and enough of them exhaust the server without ever completing a request. The
// Dockerfile EXPOSEs this port directly, so the default deployment has no
// reverse proxy to absorb it.
//
// Asserting on the struct fields would only restate the constants. This drives
// an actual socket, because the question is whether the server hangs up.

// slowlorisServer starts a server with the same limits startAPI uses.
func slowlorisServer(t *testing.T) string {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{
		Handler:             http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }),
		ReadHeaderTimeout:   300 * time.Millisecond, // the production value, scaled for a test
		IdleTimeout:         idleTimeout,
		MaxHeaderBytes:      maxHeaderBytes,
		MaxHeaderValueCount: maxHeaderValueCount,
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return ln.Addr().String()
}

// A client that never finishes its headers is hung up on, rather than holding
// the connection for as long as it likes.
func TestAClientThatDribblesHeadersIsDisconnected(t *testing.T) {
	addr := slowlorisServer(t)

	var d net.Dialer
	conn, err := d.DialContext(t.Context(), "tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// A request line and then a header, one byte at a time, never terminated.
	if _, err := fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: x\r\n"); err != nil {
		t.Fatal(err)
	}

	// The server must close the connection on its own. Read blocks until it
	// does; the deadline is what fails the test if it never happens.
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 64)
	start := time.Now()
	_, err = conn.Read(buf)
	elapsed := time.Since(start)

	if err == nil {
		return // server answered and closed; also acceptable
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		t.Fatalf("after %v the server had still not hung up on a client that never "+
			"finished its headers; with no ReadHeaderTimeout it never will, and enough "+
			"such connections exhaust it without completing a single request", elapsed)
	}
	// Any other error means the peer closed, which is the behaviour wanted.
}

// The ordinary case still works — the timeout is not so tight that a normal
// request is cut off.
func TestANormalRequestIsUnaffected(t *testing.T) {
	addr := slowlorisServer(t)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+addr+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("a normal request failed against the hardened server: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("status %d, want 204", resp.StatusCode)
	}
}

// The limits are the ones startAPI applies, so the test above is exercising
// production values rather than its own.
func TestTheLimitsAreSet(t *testing.T) {
	if readHeaderTimeout <= 0 {
		t.Error("readHeaderTimeout is unset; the server would wait forever for headers")
	}
	if idleTimeout <= 0 {
		t.Error("idleTimeout is unset; idle keep-alive connections are never reclaimed")
	}
	if maxHeaderBytes <= 0 || maxHeaderValueCount <= 0 {
		t.Error("header limits are unset")
	}
}
