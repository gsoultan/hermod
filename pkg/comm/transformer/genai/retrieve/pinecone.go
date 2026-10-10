package retrieve

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// maxPineconeReply bounds what is read from a query reply. topK is at most
// 100, so a real reply is far smaller.
const maxPineconeReply = 8 << 20

var pineconeClient = &http.Client{Timeout: 30 * time.Second}

// pinecone queries one index over its data-plane REST API.
type pinecone struct {
	host      string
	apiKey    string
	namespace string
}

func newPinecone(host, apiKey, namespace string) (*pinecone, error) {
	u, err := url.Parse(host)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, fmt.Errorf("pinecone index host %q is not an http(s) URL", host)
	}
	return &pinecone{host: strings.TrimRight(host, "/"), apiKey: apiKey, namespace: namespace}, nil
}

func (p *pinecone) query(ctx context.Context, vector []float32, k int) ([]Doc, error) {
	body := map[string]any{"vector": vector, "topK": k, "includeMetadata": true}
	if p.namespace != "" {
		body["namespace"] = p.namespace
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.host+"/query", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Api-Key", p.apiKey)
	resp, err := pineconeClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("pinecone query: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxPineconeReply))
	if err != nil {
		return nil, fmt.Errorf("pinecone query: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		excerpt := string(data)
		if len(excerpt) > 200 {
			excerpt = excerpt[:200] + "..."
		}
		return nil, fmt.Errorf("pinecone query: status %d: %s", resp.StatusCode, excerpt)
	}
	var reply struct {
		Matches []struct {
			ID       string         `json:"id"`
			Score    float64        `json:"score"`
			Metadata map[string]any `json:"metadata"`
		} `json:"matches"`
	}
	if err := json.Unmarshal(data, &reply); err != nil {
		return nil, fmt.Errorf("pinecone query: unreadable reply: %w", err)
	}
	docs := make([]Doc, 0, len(reply.Matches))
	for _, m := range reply.Matches {
		d := Doc{ID: m.ID, Score: m.Score}
		if m.Metadata != nil {
			d.Metadata = m.Metadata
		}
		docs = append(docs, d)
	}
	return docs, nil
}
