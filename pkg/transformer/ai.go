package transformer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/user/hermod"
)

func init() {
	Register("ai_enrichment", &AITransformer{})
	Register("ai_mapper", &AIMapperTransformer{})
}

// AITransformer uses Large Language Models to enrich or transform data.
type AITransformer struct {
	client *http.Client
}

// AIMapperTransformer uses Large Language Models to map data to a target schema.
type AIMapperTransformer struct {
	AITransformer
}

func (t *AIMapperTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	targetSchema, _ := config["targetSchema"].(string)
	hints, _ := config["hints"].(string)

	prompt := fmt.Sprintf("Map the following data to this JSON schema: %s", targetSchema)
	if hints != "" {
		prompt += fmt.Sprintf("\nHints: %s", hints)
	}
	prompt += "\nOutput ONLY valid JSON that matches the schema. Do not include any explanations or markdown blocks."

	config["prompt"] = prompt
	return t.AITransformer.Transform(ctx, msg, config)
}

func (t *AITransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}

	provider, _ := config["provider"].(string) // "openai", "ollama"
	endpoint, _ := config["endpoint"].(string)
	apiKey, _ := config["apiKey"].(string)
	model, _ := config["model"].(string)
	prompt, _ := config["prompt"].(string)
	targetField, _ := config["targetField"].(string)

	if endpoint == "" {
		if provider == "openai" {
			endpoint = "https://api.openai.com/v1/chat/completions"
		} else if provider == "ollama" {
			endpoint = "http://localhost:11434/api/generate"
		} else if provider == "" {
			provider = "openai"
			endpoint = "https://api.openai.com/v1/chat/completions"
		}
	}

	if t.client == nil {
		t.client = &http.Client{Timeout: 30 * time.Second}
	}

	// Prepare data for the prompt
	dataBytes, _ := json.Marshal(msg.Data())
	fullPrompt := fmt.Sprintf("%s\n\nData: %s", prompt, string(dataBytes))

	var result string
	var err error

	if provider == "openai" {
		result, err = t.callOpenAI(ctx, endpoint, apiKey, model, fullPrompt)
	} else if provider == "ollama" {
		result, err = t.callOllama(ctx, endpoint, model, fullPrompt)
	} else {
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
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("openai error (status %d): %s", resp.StatusCode, string(b))
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
		return "", fmt.Errorf("no choices returned from openai")
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
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("ollama error (status %d): %s", resp.StatusCode, string(b))
	}

	var res struct {
		Response string `json:"response"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", err
	}

	return res.Response, nil
}
