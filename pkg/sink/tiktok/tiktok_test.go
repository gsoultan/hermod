package tiktok

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/user/hermod/pkg/message"
)

func TestTikTokSink_Write(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST request, got %s", r.Method)
		}
		if r.URL.Path != "/post/publish/video/init/" {
			t.Errorf("expected /post/publish/video/init/ path, got %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("expected Bearer test-token, got %s", r.Header.Get("Authorization"))
		}

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}

		sourceInfo := body["source_info"].(map[string]any)
		if sourceInfo["video_url"] != "http://example.com/video.mp4" {
			t.Errorf("expected video_url http://example.com/video.mp4, got %v", sourceInfo["video_url"])
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{"error": nil})
	}))
	defer server.Close()

	sink := NewTikTokSink("test-token", nil)
	sink.baseURL = server.URL

	msg := message.AcquireMessage()
	msg.SetData("video_url", "http://example.com/video.mp4")
	msg.SetData("title", "Test Video")

	err := sink.Write(context.Background(), msg)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
}

func TestTikTokSink_Ping(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/user/info/" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	sink := NewTikTokSink("test-token", nil)
	sink.baseURL = server.URL

	err := sink.Ping(context.Background())
	if err != nil {
		t.Fatalf("Ping failed: %v", err)
	}
}
