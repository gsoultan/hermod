package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gsoultan/hermod/pkg/comm/transformer"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"

	"github.com/gsoultan/hermod"
)

func init() {
	transformer.Register("ai_enrichment", &AITransformer{})
}

// AITransformer uses Large Language Models to enrich or transform data.
//
// One registered value serves every message of every workflow, so it holds
// nothing that is written after construction.
type AITransformer struct {
	client *http.Client
}

// defaultAIClient is used when a transformer was built without its own client.
var defaultAIClient = &http.Client{Timeout: 30 * time.Second}

// maxAIErrorBody is how much of a provider's error response is kept in the
// returned error. Providers can answer with whole HTML pages.
const maxAIErrorBody = 2 << 10

func (t *AITransformer) httpClient() *http.Client {
	if t.client != nil {
		return t.client
	}
	return defaultAIClient
}

// errorBody reads at most maxAIErrorBody bytes of an error response.
func errorBody(r io.Reader) string {
	b, _ := io.ReadAll(io.LimitReader(r, maxAIErrorBody))
	return string(b)
}

func (t *AITransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}

	provider, _ := config["provider"].(string) // "openai", "ollama"
	endpoint, _ := config["endpoint"].(string)
	apiKey, _ := config["apiKey"].(string)
	// The key decides whose account is billed, so row data must not choose
	// it: only {{secret("NAME")}} resolves, for the message's vhost.
	apiKey = strings.TrimSpace(evaluator.ResolveTemplateScoped(apiKey, msg))
	model, _ := config["model"].(string)
	prompt, _ := config["prompt"].(string)
	targetField, _ := config["targetField"].(string)

	if endpoint == "" {
		switch provider {
		case "openai":
			endpoint = "https://api.openai.com/v1/chat/completions"
		case "ollama":
			endpoint = "http://localhost:11434/api/generate"
		case "":
			provider = "openai"
			endpoint = "https://api.openai.com/v1/chat/completions"
		}
	}

	// Prepare data for the prompt
	dataBytes, _ := json.Marshal(msg.Data())
	fullPrompt := fmt.Sprintf("%s\n\nData: %s", prompt, string(dataBytes))

	var result string
	var err error

	switch provider {
	case "openai":
		result, err = t.callOpenAI(ctx, endpoint, apiKey, model, fullPrompt)
	case "ollama":
		result, err = t.callOllama(ctx, endpoint, model, fullPrompt)
	default:
		return nil, fmt.Errorf("unsupported AI provider: %s", provider)
	}

	if err != nil {
		return nil, fmt.Errorf("AI transformation failed: %w", err)
	}

	if targetField != "" {
		msg.SetData(targetField, result)
	} else {
		// If no target field, try to parse result as JSON and merge into data
		var resultMap map[string]any
		if err := json.Unmarshal([]byte(result), &resultMap); err == nil {
			for k, v := range resultMap {
				msg.SetData(k, v)
			}
		} else {
			msg.SetData("ai_result", result)
		}
	}

	return msg, nil
}

func (t *AITransformer) callOpenAI(ctx context.Context, endpoint, apiKey, model, prompt string) (string, error) {
	if model == "" {
		model = "gpt-3.5-turbo"
	}

	reqBody := map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	}

	bodyBytes, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := t.httpClient().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("openai error (status %d): %s", resp.StatusCode, errorBody(resp.Body))
	}

	var res struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", err
	}

	if len(res.Choices) == 0 {
		return "", errors.New("no choices returned from openai")
	}

	return res.Choices[0].Message.Content, nil
}

func (t *AITransformer) callOllama(ctx context.Context, endpoint, model, prompt string) (string, error) {
	if model == "" {
		model = "llama2"
	}

	reqBody := map[string]any{
		"model":  model,
		"prompt": prompt,
		"stream": false,
	}

	bodyBytes, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := t.httpClient().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ollama error (status %d): %s", resp.StatusCode, errorBody(resp.Body))
	}

	var res struct {
		Response string `json:"response"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", err
	}

	return res.Response, nil
}
