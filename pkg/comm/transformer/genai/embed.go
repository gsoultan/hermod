package genai

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/transformer"
	"github.com/gsoultan/hermod/pkg/comm/transformer/core"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
	"github.com/gsoultan/hermod/pkg/llm"
)

func init() {
	transformer.Register("ai_embed", &EmbedTransformer{})
}

// DefaultEmbeddingField is where ai_embed writes the vector by default.
const DefaultEmbeddingField = "embedding"

// EmbedTransformer writes an embedding vector for one field (inputField) or
// a template (text) into targetField, ready for a pgvector, Pinecone or
// Milvus sink.
type EmbedTransformer struct{}

// Transform implements transformer.Transformer.
func (t *EmbedTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}
	var text string
	if field := core.GetConfigString(config, "inputField"); field != "" {
		text = fmt.Sprint(msg.Data()[field])
	} else if tmpl := core.GetConfigString(config, "text"); tmpl != "" {
		text = evaluator.ResolveTemplateMsg(tmpl, msg)
	}
	if strings.TrimSpace(text) == "" || text == "<nil>" {
		return nil, errors.New("ai_embed: nothing to embed (set inputField or text)")
	}
	p, model, err := ProviderFor(config, msg)
	if err != nil {
		return nil, fmt.Errorf("ai_embed: %w", err)
	}
	e, ok := p.(llm.Embedder)
	if !ok {
		return nil, fmt.Errorf("ai_embed: %s cannot produce embeddings", p.Name())
	}
	_, _, timeout := callOptions(config)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	resp, err := e.Embed(ctx, llm.EmbedRequest{Model: model, Inputs: []string{text}})
	if err != nil {
		if errors.Is(err, llm.ErrUnsupported) {
			return nil, fmt.Errorf("ai_embed: %s cannot produce embeddings", p.Name())
		}
		return nil, fmt.Errorf("ai_embed: %w", err)
	}
	if len(resp.Vectors) != 1 || len(resp.Vectors[0]) == 0 {
		return nil, errors.New("ai_embed: the provider returned no vector")
	}
	target := core.GetConfigString(config, "targetField")
	if target == "" {
		target = DefaultEmbeddingField
	}
	msg.SetData(target, resp.Vectors[0])
	return msg, nil
}
