package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/user/hermod"
)

type mockMessage struct {
	hermod.Message
	id string
}

func (m *mockMessage) ID() string                   { return m.id }
func (m *mockMessage) Operation() hermod.Operation  { return hermod.OpCreate }
func (m *mockMessage) Table() string                { return "test_table" }
func (m *mockMessage) Schema() string               { return "test_schema" }
func (m *mockMessage) Data() map[string]interface{} { return nil }
func (m *mockMessage) Clone() hermod.Message        { return m }
func (m *mockMessage) ClearPayloads()               {}

type mockFormatter struct{}

func (f *mockFormatter) Format(msg hermod.Message) ([]byte, error) {
	return []byte("formatted message"), nil
}

func TestHttpSink_Write(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST method, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sink := NewHttpSink(server.URL, &mockFormatter{}, map[string]string{"X-Test": "Value"})
	err := sink.Write(context.Background(), &mockMessage{id: "123"})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestHttpSink_Ping(t *testing.T) {
	t.Run("default HEAD", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "HEAD" {
				t.Errorf("expected HEAD method, got %s", r.Method)
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		sink := NewHttpSink(server.URL, &mockFormatter{}, nil)
		err := sink.Ping(context.Background())
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("custom GET", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "GET" {
				t.Errorf("expected GET method, got %s", r.Method)
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		sink := NewHttpSink(server.URL, &mockFormatter{}, nil)
		sink.SetPingMethod("GET")
		err := sink.Ping(context.Background())
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}
