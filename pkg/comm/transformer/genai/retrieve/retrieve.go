// Package retrieve is the ai_retrieve (RAG) transformer: it embeds a query
// with the node's AI connection, asks a vector store for the nearest
// documents, and writes them with their scores into the message, ready for a
// following ai_prompt node (or an ai_agent tool).
//
// Stores: pgvector (the table a pgvector sink writes) and Pinecone (an index
// a Pinecone sink writes). Milvus is not covered yet.
package retrieve

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/transformer"
	"github.com/gsoultan/hermod/pkg/comm/transformer/core"
	"github.com/gsoultan/hermod/pkg/comm/transformer/genai"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
	"github.com/gsoultan/hermod/pkg/llm"
)

func init() {
	transformer.Register("ai_retrieve", &Transformer{})
}

const (
	defaultTopK = 5
	maxTopK     = 100
	// DefaultTargetField is where the documents go by default.
	DefaultTargetField = "ai_context"
	defaultTimeout     = 30 * time.Second
)

// Doc is one retrieved document. Score is higher for closer documents.
type Doc struct {
	ID       string  `json:"id"`
	Score    float64 `json:"score"`
	Content  any     `json:"content,omitempty"`
	Metadata any     `json:"metadata,omitempty"`
}

// store answers a nearest-neighbour query.
type store interface {
	query(ctx context.Context, vector []float32, k int) ([]Doc, error)
}

// Transformer is ai_retrieve.
//
// Config: query (a template over the message) or queryField (a field read
// verbatim), store ("pgvector" or "pinecone"), topK (default 5, at most 100),
// minScore, targetField, the embedding connection keys read by
// genai.ProviderFor (provider, model, apiKey, baseUrl), and the store's keys:
//
//   - pgvector: connectionString, table, vectorColumn (default "embedding"),
//     idColumn (default "id"), contentColumn, metadataColumn (default
//     "metadata"), metric ("cosine", "l2" or "inner_product").
//   - pinecone: indexHost, storeApiKey, namespace.
//
// connectionString, indexHost and storeApiKey decide where a credential is
// sent, so they resolve with evaluator.ResolveTemplateScoped: a vhost secret
// works there, row data does not.
type Transformer struct{}

// Transform implements transformer.Transformer.
func (t *Transformer) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}
	s, err := openStore(config, msg)
	if err != nil {
		return nil, fmt.Errorf("ai_retrieve: %w", err)
	}
	text := queryText(config, msg)
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("ai_retrieve: nothing to search for (set query or queryField)")
	}

	timeout := defaultTimeout
	if d, err := time.ParseDuration(core.GetConfigString(config, "timeout")); err == nil && d > 0 {
		timeout = d
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	vector, err := embed(ctx, config, msg, text)
	if err != nil {
		return nil, fmt.Errorf("ai_retrieve: %w", err)
	}
	docs, err := s.query(ctx, vector, topK(config))
	if err != nil {
		return nil, fmt.Errorf("ai_retrieve: %w", err)
	}
	target := core.GetConfigString(config, "targetField")
	if target == "" {
		target = DefaultTargetField
	}
	msg.SetData(target, docsValue(docs, config))
	return msg, nil
}

// docsValue is the documents as message data, without those below minScore.
func docsValue(docs []Doc, config map[string]any) []any {
	minScore, filter := number(config["minScore"])
	out := make([]any, 0, len(docs))
	for _, d := range docs {
		if filter && d.Score < minScore {
			continue
		}
		item := map[string]any{"id": d.ID, "score": d.Score}
		if d.Content != nil {
			item["content"] = d.Content
		}
		if d.Metadata != nil {
			item["metadata"] = d.Metadata
		}
		out = append(out, item)
	}
	return out
}

func queryText(config map[string]any, msg hermod.Message) string {
	if field := core.GetConfigString(config, "queryField"); field != "" {
		if v, ok := msg.Data()[field]; ok && v != nil {
			return fmt.Sprint(v)
		}
		return ""
	}
	return evaluator.ResolveTemplateMsg(core.GetConfigString(config, "query"), msg)
}

func embed(ctx context.Context, config map[string]any, msg hermod.Message, text string) ([]float32, error) {
	p, model, err := genai.ProviderFor(config, msg)
	if err != nil {
		return nil, err
	}
	e, ok := p.(llm.Embedder)
	if !ok {
		return nil, fmt.Errorf("%s cannot produce embeddings", p.Name())
	}
	resp, err := e.Embed(ctx, llm.EmbedRequest{Model: model, Inputs: []string{text}})
	if err != nil {
		if errors.Is(err, llm.ErrUnsupported) {
			return nil, fmt.Errorf("%s cannot produce embeddings", p.Name())
		}
		return nil, err
	}
	if len(resp.Vectors) != 1 || len(resp.Vectors[0]) == 0 {
		return nil, errors.New("the provider returned no vector")
	}
	return resp.Vectors[0], nil
}

func openStore(config map[string]any, msg hermod.Message) (store, error) {
	scoped := func(key string) string {
		return strings.TrimSpace(evaluator.ResolveTemplateScoped(core.GetConfigString(config, key), msg))
	}
	str := func(key, def string) string {
		if v := strings.TrimSpace(core.GetConfigString(config, key)); v != "" {
			return v
		}
		return def
	}
	switch kind := strings.ToLower(core.GetConfigString(config, "store")); kind {
	case "pinecone":
		host := scoped("indexHost")
		if host == "" {
			return nil, errors.New("pinecone needs the index host (indexHost)")
		}
		return newPinecone(host, scoped("storeApiKey"), core.GetConfigString(config, "namespace"))
	case "pgvector":
		conn := scoped("connectionString")
		if conn == "" {
			return nil, errors.New("pgvector needs a connection string (connectionString)")
		}
		target := pgvectorTarget{
			table:          core.GetConfigString(config, "table"),
			vectorColumn:   str("vectorColumn", "embedding"),
			idColumn:       str("idColumn", "id"),
			contentColumn:  core.GetConfigString(config, "contentColumn"),
			metadataColumn: str("metadataColumn", "metadata"),
			metric:         str("metric", "cosine"),
		}
		return newPgvector(conn, target)
	case "":
		return nil, errors.New("a vector store is required (store: pgvector or pinecone)")
	default:
		return nil, fmt.Errorf("vector store %q is not supported; use pgvector or pinecone", kind)
	}
}

func topK(config map[string]any) int {
	n, ok := number(config["topK"])
	if !ok || n < 1 {
		return defaultTopK
	}
	return int(min(n, maxTopK))
}

func number(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case string:
		var f float64
		_, err := fmt.Sscan(strings.TrimSpace(n), &f)
		return f, err == nil
	}
	return 0, false
}
