package ai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/comm/transformer"
)

// One registered transformer serves every message of every workflow that
// uses it, so Transform runs concurrently on the same value. Its HTTP client
// was created lazily on first use, which is a write racing with reads.
func TestAITransformer_ConcurrentFirstUseIsRaceFree(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": "ok"}}},
		})
	}))
	defer srv.Close()

	tf := &AITransformer{}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			msg := message.AcquireMessage()
			msg.SetData("text", "x")
			cfg := map[string]any{"provider": "openai", "endpoint": srv.URL, "targetField": "out"}
			if _, err := tf.Transform(t.Context(), msg, cfg); err != nil {
				t.Errorf("Transform: %v", err)
			}
		})
	}
	wg.Wait()
}

// A provider that answers an error with a huge body must not have all of it
// read into memory and copied into the error (and from there into logs).
func TestAITransformer_ErrorBodyIsBounded(t *testing.T) {
	big := strings.Repeat("x", 4<<20)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(big))
	}))
	defer srv.Close()

	msg := message.AcquireMessage()
	msg.SetData("text", "x")
	_, err := (&AITransformer{}).Transform(t.Context(), msg, map[string]any{"provider": "openai", "endpoint": srv.URL})
	if err == nil {
		t.Fatal("expected an error for a 502")
	}
	if len(err.Error()) > 8<<10 {
		t.Fatalf("error carries %d bytes of the response body; want it capped", len(err.Error()))
	}
}

// ai_mapper must never write into the node's config: that map is shared by
// every message the node handles.
func TestAIMapper_DoesNotMutateNodeConfig(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": `{"a":1}`}}},
		})
	}))
	defer srv.Close()

	tf, ok := transformer.Get("ai_mapper")
	if !ok {
		t.Fatal("ai_mapper is not registered")
	}
	cfg := map[string]any{"provider": "openai", "endpoint": srv.URL, "targetSchema": "{}"}
	msg := message.AcquireMessage()
	msg.SetData("text", "x")
	if _, err := tf.Transform(t.Context(), msg, cfg); err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if _, wrote := cfg["prompt"]; wrote {
		t.Fatal("ai_mapper wrote prompt into the shared node config")
	}
}
