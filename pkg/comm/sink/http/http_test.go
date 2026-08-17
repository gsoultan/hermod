package http

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/user/hermod"
)

type mockMessage struct {
	hermod.Message
	id string
}

func (m *mockMessage) ID() string                     { return m.id }
func (m *mockMessage) Operation() hermod.Operation    { return hermod.OpCreate }
func (m *mockMessage) Table() string                  { return "test_table" }
func (m *mockMessage) Schema() string                 { return "test_schema" }
func (m *mockMessage) Data() map[string]any           { return nil }
func (m *mockMessage) MetadataRef() map[string]string { return nil }
func (m *mockMessage) DataRef() map[string]any        { return nil }
func (m *mockMessage) Clone() hermod.Message          { return m }
func (m *mockMessage) ToMap() map[string]any          { return nil }
func (m *mockMessage) ClearPayloads()                 {}

type mockFormatter struct{}

func (f *mockFormatter) Format(msg hermod.Message) ([]byte, error) {
	return []byte("formatted message"), nil
}

func TestHttpSink_Write(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST method, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sink := NewHttpSink(server.URL, &mockFormatter{}, map[string]string{"X-Test": "Value"})
	err := sink.Write(t.Context(), &mockMessage{id: "123"})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// A server that accepts the connection and then never answers must not hang
// Write forever. The engine retries a failed write; it cannot retry one that
// never returns, and a sink with no timeout turns one silent endpoint into a
// permanently stalled pipeline — no error, no log, nothing to alert on.
func TestAServerThatNeverRespondsDoesNotHangWrite(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer server.Close()
	defer close(release)

	sink := NewHttpSink(server.URL, &mockFormatter{}, nil)
	sink.SetTimeout(100 * time.Millisecond)

	done := make(chan error, 1)
	go func() { done <- sink.Write(context.Background(), &mockMessage{id: "1"}) }()

	select {
	case err := <-done:
		if err == nil {
			t.Error("a write the server never answered reported success")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Write is still blocked on a server that will never answer; " +
			"the sink has no timeout of its own, so only a caller-supplied " +
			"deadline can unblock it — and the engine does not supply one")
	}
}

// The default client must carry a timeout without anyone configuring it. The
// test above proves SetTimeout works; this pins that a sink built with the
// bare constructor is protected too, because nobody sets what they have not
// been told exists.
func TestTheDefaultClientHasATimeout(t *testing.T) {
	sink := NewHttpSink("http://example.invalid", nil, nil)
	if sink.client.Timeout == 0 {
		t.Error("a sink built with defaults has no timeout, so a silent " +
			"endpoint blocks Write forever")
	}
}

// What actually goes over the wire: the formatted body, the caller's headers,
// and a Content-Type. The existing test asserted only the method, so a sink
// that sent an empty body to the right URL passed. WriteBatch already defaults
// Content-Type to application/json; a single Write sent none at all, and a
// receiver that rejects untyped requests with 415 refuses every single-message
// delivery while accepting batches.
func TestWriteSendsBodyHeadersAndAContentType(t *testing.T) {
	var gotBody []byte
	var gotContentType, gotCustom string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotContentType = r.Header.Get("Content-Type")
		gotCustom = r.Header.Get("X-Test")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sink := NewHttpSink(server.URL, &mockFormatter{}, map[string]string{"X-Test": "Value"})
	if err := sink.Write(t.Context(), &mockMessage{id: "1"}); err != nil {
		t.Fatalf("write: %v", err)
	}

	if string(gotBody) != "formatted message" {
		t.Errorf("the server received %q, want the formatted message", gotBody)
	}
	if gotCustom != "Value" {
		t.Errorf("the X-Test header arrived as %q, want Value", gotCustom)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type arrived as %q, want the application/json default "+
			"WriteBatch already sends", gotContentType)
	}
}

// A Content-Type the caller set must win over the default.
func TestAUserSuppliedContentTypeIsKept(t *testing.T) {
	var gotContentType string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sink := NewHttpSink(server.URL, &mockFormatter{}, map[string]string{"Content-Type": "text/csv"})
	if err := sink.Write(t.Context(), &mockMessage{id: "1"}); err != nil {
		t.Fatalf("write: %v", err)
	}
	if gotContentType != "text/csv" {
		t.Errorf("the caller's Content-Type %q was replaced with %q", "text/csv", gotContentType)
	}
}

// A non-2xx answer is a failed write. The engine's retry and DLQ logic hang off
// this error; a sink that swallowed a 500 would acknowledge data the far side
// refused.
func TestANon2xxResponseIsAnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusInternalServerError)
	}))
	defer server.Close()

	sink := NewHttpSink(server.URL, &mockFormatter{}, nil)
	if err := sink.Write(t.Context(), &mockMessage{id: "1"}); err == nil {
		t.Error("a 500 from the server was reported as a successful write")
	}
	if err := sink.WriteBatch(t.Context(), []hermod.Message{&mockMessage{id: "1"}}); err == nil {
		t.Error("a 500 from the server was reported as a successful batch write")
	}
}

func TestHttpSink_Ping(t *testing.T) {
	t.Run("default HEAD", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodHead {
				t.Errorf("expected HEAD method, got %s", r.Method)
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		sink := NewHttpSink(server.URL, &mockFormatter{}, nil)
		err := sink.Ping(t.Context())
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("custom GET", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Errorf("expected GET method, got %s", r.Method)
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		sink := NewHttpSink(server.URL, &mockFormatter{}, nil)
		sink.SetPingMethod("GET")
		err := sink.Ping(t.Context())
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}
