package http

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/user/hermod/pkg/compression"
	"github.com/user/hermod/pkg/message"
)

func TestHttpSinkCompression(t *testing.T) {
	testData := "this is a long enough message to trigger compression. "
	for i := 0; i < 200; i++ {
		testData += "more data to make it much larger than 1024 bytes "
	}

	algorithms := []compression.Algorithm{compression.LZ4, compression.Snappy, compression.Zstd}

	for _, algo := range algorithms {
		t.Run(string(algo), func(t *testing.T) {
			serverCalled := false
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				serverCalled = true

				encoding := r.Header.Get("Content-Encoding")
				if encoding != string(algo) {
					t.Errorf("expected Content-Encoding %s, got %s", algo, encoding)
				}

				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Fatalf("failed to read body: %v", err)
				}

				comp, err := compression.NewCompressor(algo)
				if err != nil {
					t.Fatalf("failed to create compressor: %v", err)
				}

				decompressed, err := comp.Decompress(body)
				if err != nil {
					t.Fatalf("failed to decompress: %v", err)
				}

				if string(decompressed) != testData {
					t.Errorf("expected data %s, got %s", testData, string(decompressed))
				}

				w.WriteHeader(http.StatusOK)
			}))
			defer ts.Close()

			sink := NewHttpSink(ts.URL, nil, nil)
			comp, _ := compression.NewCompressor(algo)
			sink.SetCompressor(comp)

			msg := message.AcquireMessage()
			msg.SetPayload([]byte(testData))

			err := sink.Write(context.Background(), msg)
			if err != nil {
				t.Fatalf("Write failed: %v", err)
			}

			if !serverCalled {
				t.Error("server was not called")
			}
		})
	}
}
