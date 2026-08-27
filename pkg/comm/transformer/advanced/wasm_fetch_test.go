package advanced

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Where the WASM transformer gets its module from.
//
// Prepare downloaded the module with http.Get, which has no timeout —
// http.DefaultClient's is zero — and performs no SSRF check. The URL comes
// from configuration, so a server that accepted and then stalled held the
// call forever, and a URL pointing at an internal address made the server
// fetch on the caller's behalf.
//
// Both matter more here than almost anywhere else in this codebase, because
// what arrives is not data: it is a WebAssembly module about to be compiled
// and executed.

// A module URL pointing at a private address is refused rather than fetched.
// This is the SSRF case, and it is the reason this fetch uses the guarded
// client while data-source fetches deliberately do not: reading from an
// internal HTTP service is ordinary for a self-hosted pipeline, but pulling
// executable code from one is not.
func TestAModuleURLPointingInsideTheNetworkIsRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("\x00asm\x01\x00\x00\x00"))
	}))
	defer srv.Close()

	tr := &WasmTransformer{}
	_, err := tr.Prepare(map[string]any{"wasmURL": srv.URL})

	if err == nil {
		t.Fatal("a module was fetched from a loopback address; the URL comes from " +
			"configuration, and what arrives here is executed")
	}
	if !strings.Contains(err.Error(), "blocked") && !strings.Contains(err.Error(), "private") {
		t.Errorf("the fetch failed, but not because the address was refused: %v", err)
	}
}

// A server that accepts and never answers must not hold the call forever.
func TestAStalledModuleServerDoesNotHangPrepare(t *testing.T) {
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
			// Accept and say nothing at all.
			_ = c
		}
	}()

	tr := &WasmTransformer{}
	done := make(chan struct{})
	go func() {
		_, _ = tr.Prepare(map[string]any{"wasmURL": "http://" + ln.Addr().String() + "/m.wasm"})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("Prepare is still waiting on a server that will never answer; with " +
			"http.Get there is no timeout at all, so it never returns and takes the " +
			"worker preparing the transformer with it")
	}
}
