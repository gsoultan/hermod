package retrieve

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/comm/transformer"
)

// embedServer is an OpenAI-compatible embeddings endpoint that answers every
// input with the same vector and records the inputs.
func embedServer(t *testing.T, inputs *[]string) string {
	t.Helper()
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		*inputs = append(*inputs, body.Input...)
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []any{map[string]any{"index": 0, "embedding": []float32{0.1, 0.2, 0.3}}},
		})
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

type pineconeCall struct {
	path   string
	apiKey string
	body   map[string]any
}

func pineconeServer(t *testing.T, calls *[]pineconeCall) string {
	t.Helper()
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		*calls = append(*calls, pineconeCall{path: r.URL.Path, apiKey: r.Header.Get("Api-Key"), body: body})
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"matches": []any{
			map[string]any{"id": "doc-1", "score": 0.91, "metadata": map[string]any{"text": "Refunds take 5 days."}},
			map[string]any{"id": "doc-2", "score": 0.72, "metadata": map[string]any{"text": "Shipping is free."}},
		}})
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func msgWith(t *testing.T, data map[string]any) hermod.Message {
	t.Helper()
	m := message.AcquireMessage()
	m.SetID("m1")
	for k, v := range data {
		m.SetData(k, v)
	}
	t.Cleanup(m.Release)
	return m
}

func pineconeConfig(embedURL, host string, extra map[string]any) map[string]any {
	cfg := map[string]any{
		"provider": "openai_compatible", "baseUrl": embedURL, "model": "embed-1",
		"store": "pinecone", "indexHost": host, "storeApiKey": "pc-key", "namespace": "kb",
		"query": "{{.question}}", "topK": float64(2),
	}
	maps.Copy(cfg, extra)
	return cfg
}

func TestRetrieve_IsRegistered(t *testing.T) {
	if _, ok := transformer.Get("ai_retrieve"); !ok {
		t.Fatal("ai_retrieve is not registered")
	}
}

func TestRetrieve_PineconeTopKGoesIntoTheTargetField(t *testing.T) {
	var inputs []string
	var calls []pineconeCall
	cfg := pineconeConfig(embedServer(t, &inputs), pineconeServer(t, &calls), map[string]any{"targetField": "context"})
	msg := msgWith(t, map[string]any{"question": "how long do refunds take?"})

	out, err := (&Transformer{}).Transform(t.Context(), msg, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 1 || inputs[0] != "how long do refunds take?" {
		t.Fatalf("embedded %q", inputs)
	}
	if len(calls) != 1 || calls[0].path != "/query" || calls[0].apiKey != "pc-key" {
		t.Fatalf("pinecone calls = %+v", calls)
	}
	body := calls[0].body
	if body["topK"] != float64(2) || body["namespace"] != "kb" || body["includeMetadata"] != true {
		t.Fatalf("query body = %v", body)
	}
	if vec, _ := body["vector"].([]any); len(vec) != 3 {
		t.Fatalf("query vector = %v", body["vector"])
	}
	docs, ok := out.Data()["context"].([]any)
	if !ok || len(docs) != 2 {
		t.Fatalf("context = %#v", out.Data()["context"])
	}
	first, _ := docs[0].(map[string]any)
	meta, _ := first["metadata"].(map[string]any)
	if first["id"] != "doc-1" || first["score"] != 0.91 || meta["text"] != "Refunds take 5 days." {
		t.Fatalf("first doc = %v", first)
	}
}

func TestRetrieve_MinScoreDropsDistantDocuments(t *testing.T) {
	var inputs []string
	var calls []pineconeCall
	cfg := pineconeConfig(embedServer(t, &inputs), pineconeServer(t, &calls), map[string]any{"minScore": "0.8"})
	out, err := (&Transformer{}).Transform(t.Context(), msgWith(t, map[string]any{"question": "q"}), cfg)
	if err != nil {
		t.Fatal(err)
	}
	docs, _ := out.Data()[DefaultTargetField].([]any)
	if len(docs) != 1 || docs[0].(map[string]any)["id"] != "doc-1" {
		t.Fatalf("docs = %v", docs)
	}
}

func TestRetrieve_QueryFieldIsReadVerbatim(t *testing.T) {
	var inputs []string
	var calls []pineconeCall
	cfg := pineconeConfig(embedServer(t, &inputs), pineconeServer(t, &calls), map[string]any{"query": "", "queryField": "q"})
	if _, err := (&Transformer{}).Transform(t.Context(), msgWith(t, map[string]any{"q": "{{.secret}}"}), cfg); err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 1 || inputs[0] != "{{.secret}}" {
		t.Fatalf("embedded %q", inputs)
	}
}

// The host and the key decide where a credential goes, so row data cannot
// fill them: field tokens render empty there.
func TestRetrieve_RowDataCannotChooseTheHostOrTheKey(t *testing.T) {
	var hit atomic.Int32
	evil := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit.Add(1) }))
	t.Cleanup(evil.Close)

	var inputs []string
	cfg := pineconeConfig(embedServer(t, &inputs), "{{.host}}", nil)
	msg := msgWith(t, map[string]any{"question": "q", "host": evil.URL})
	if _, err := (&Transformer{}).Transform(t.Context(), msg, cfg); err == nil {
		t.Fatal("a host taken from row data must not be used")
	}
	if hit.Load() != 0 {
		t.Fatal("the request went to a host the row chose")
	}

	var calls []pineconeCall
	cfg = pineconeConfig(embedServer(t, &inputs), pineconeServer(t, &calls), map[string]any{"storeApiKey": "{{.key}}"})
	if _, err := (&Transformer{}).Transform(t.Context(), msgWith(t, map[string]any{"question": "q", "key": "stolen"}), cfg); err != nil {
		t.Fatal(err)
	}
	if calls[0].apiKey != "" {
		t.Fatalf("api key came from row data: %q", calls[0].apiKey)
	}
}

func TestRetrieve_TopKIsBounded(t *testing.T) {
	cases := map[any]int{nil: defaultTopK, float64(0): defaultTopK, "3": 3, float64(1e6): maxTopK}
	for in, want := range cases {
		if got := topK(map[string]any{"topK": in}); got != want {
			t.Errorf("topK(%v) = %d, want %d", in, got, want)
		}
	}
}

func TestRetrieve_ConfigErrors(t *testing.T) {
	var inputs []string
	embed := embedServer(t, &inputs)
	cases := map[string]map[string]any{
		"no store":         {"store": ""},
		"unknown store":    {"store": "qdrant"},
		"no query":         {"query": ""},
		"pinecone no host": {"indexHost": ""},
		"pgvector no conn": {"store": "pgvector"},
		"pgvector bad table": {
			"store": "pgvector", "connectionString": "postgres://u:p@127.0.0.1:1/db", "table": "docs; DROP TABLE x",
		},
	}
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := pineconeConfig(embed, "http://127.0.0.1:1", extra)
			if _, err := (&Transformer{}).Transform(t.Context(), msgWith(t, map[string]any{"question": "q"}), cfg); err == nil {
				t.Fatal("want an error")
			}
		})
	}
}

func TestPgvectorQuery_QuotesIdentifiersAndBindsTheVector(t *testing.T) {
	q, err := pgvectorQuery(pgvectorTarget{
		table: "public.docs", vectorColumn: "embedding", idColumn: "id", contentColumn: "body", metadataColumn: "metadata", metric: "cosine",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"public"."docs"`, `"embedding" <=> $1::vector`, `"body"`, `"metadata"`, `LIMIT $2`} {
		if !strings.Contains(q, want) {
			t.Errorf("query misses %s:\n%s", want, q)
		}
	}
	for metric, op := range map[string]string{"l2": "<->", "inner_product": "<#>"} {
		q, err := pgvectorQuery(pgvectorTarget{table: "docs", vectorColumn: "v", idColumn: "id", metadataColumn: "m", metric: metric})
		if err != nil || !strings.Contains(q, op) {
			t.Errorf("%s: err=%v query=%s", metric, err, q)
		}
	}
	if _, err := pgvectorQuery(pgvectorTarget{table: "docs", vectorColumn: "v\"; DROP", idColumn: "id", metric: "cosine"}); err == nil {
		t.Error("a hostile identifier must be refused")
	}
	if _, err := pgvectorQuery(pgvectorTarget{table: "docs", vectorColumn: "v", idColumn: "id", metric: "hamming"}); err == nil {
		t.Error("an unknown metric must be refused")
	}
}

func TestPgvectorVectorLiteral(t *testing.T) {
	if got := vectorLiteral([]float32{0.5, -1, 2.25}); got != "[0.5,-1,2.25]" {
		t.Fatalf("vectorLiteral = %q", got)
	}
}
