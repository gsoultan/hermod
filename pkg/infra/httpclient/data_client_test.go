package httpclient

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// DataClient is what operator-configured fetches and posts use.
//
// They used http.DefaultClient and http.Get, whose timeout is zero: a server
// that accepted the connection and then never replied held the caller forever,
// and in a pipeline that is a wedged worker rather than a slow request.
//
// It is a separate client from DefaultClient on purpose, and the difference is
// the point of these tests: DefaultClient refuses private addresses, which is
// right for fetching code and wrong for a self-hosted pipeline reading from an
// internal service.

// A server that accepts and never answers must not hold the caller forever.
func TestAStalledServerDoesNotHoldTheCaller(t *testing.T) {
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c // accepted, and deliberately never answered
		}
	}()

	c := NewDataClient()
	// Shorten the header wait so the test does not sit for the production
	// minute; the behaviour under test is that the wait is bounded at all.
	c.Transport.(*http.Transport).ResponseHeaderTimeout = 300 * time.Millisecond

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
		"http://"+ln.Addr().String()+"/x", nil)
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		resp, err := c.Do(req)
		if resp != nil {
			resp.Body.Close()
		}
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Error("a server that never replied returned success")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the client is still waiting on a server that will never answer; " +
			"http.DefaultClient has no timeout at all, so it never stops")
	}
}

// An ordinary request still works, and is not cut off by the bounds above.
func TestAnOrdinaryRequestIsUnaffected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := DataClient.Do(req)
	if err != nil {
		t.Fatalf("an ordinary request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status %d, want 200", resp.StatusCode)
	}
}

// The distinction that justifies two clients: DataClient reaches a loopback
// address, DefaultClient refuses one. A self-hosted pipeline reading from an
// internal service is ordinary; fetching executable code from one is not.
func TestDataClientReachesInternalAddressesAndDefaultClientDoesNot(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := DataClient.Do(req)
	if err != nil {
		t.Fatalf("DataClient refused an internal address; a source pointed at an "+
			"internal service is an ordinary thing to configure: %v", err)
	}
	resp.Body.Close()

	req2, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp2, err := DefaultClient.Do(req2)
	if err == nil {
		resp2.Body.Close()
		t.Error("DefaultClient reached a loopback address; it is the client used for " +
			"fetching code, and its whole purpose is to refuse that")
	}
}
