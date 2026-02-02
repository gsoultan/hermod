package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/user/hermod"
)

type FixSuggestion struct {
	Explanation string  `json:"explanation"`
	FixAction   string  `json:"fix_action"` // "update_mapping", "add_node", "change_config"
	ConfigPatch any     `json:"config_patch,omitempty"`
	Confidence  float64 `json:"confidence"`
}

type SelfHealingService struct {
	logger hermod.Logger
	apiKey string
	model  string
}

func NewSelfHealingService(logger hermod.Logger) *SelfHealingService {
	return &SelfHealingService{
		logger: logger,
		apiKey: os.Getenv("OPENAI_API_KEY"),
		model:  os.Getenv("AI_MODEL"), // e.g. "gpt-4" or "ollama/llama3"
	}
}

func (s *SelfHealingService) callLLM(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	if s.apiKey == "" && !strings.HasPrefix(s.model, "ollama") {
		return "", fmt.Errorf("AI service not configured: OPENAI_API_KEY is missing")
	}

	url := "https://api.openai.com/v1/chat/completions"
	if strings.HasPrefix(s.model, "ollama") {
		url = "http://localhost:11434/v1/chat/completions"
	}

	payload := map[string]any{
		"model": s.model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userPrompt},
		},
	}

	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}

	if s.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.apiKey)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("AI service returned status %d", resp.StatusCode)
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
		return "", fmt.Errorf("AI service returned no choices")
	}

	return res.Choices[0].Message.Content, nil
}

func (s *SelfHealingService) AnalyzeError(ctx context.Context, workflowID string, nodeID string, errStr string, sampleData any) (*FixSuggestion, error) {
	if s.apiKey == "" && !strings.HasPrefix(s.model, "ollama") {
		// Fallback to heuristics if not configured
		return s.analyzeErrorHeuristics(workflowID, nodeID, errStr)
	}

	s.logger.Info("AI calling LLM for self-healing", "workflow_id", workflowID, "node_id", nodeID)

	systemPrompt := "You are a data engineering expert. Analyze the error and sample data, then suggest a fix in JSON format with fields: explanation, fix_action (update_mapping, add_node, change_config), and confidence (0-1)."
	userPrompt := fmt.Sprintf("Workflow: %s\nNode: %s\nError: %s\nSample Data: %+v", workflowID, nodeID, errStr, sampleData)

	content, err := s.callLLM(ctx, systemPrompt, userPrompt)
	if err != nil {
		s.logger.Error("AI LLM call failed, falling back to heuristics", "error", err)
		return s.analyzeErrorHeuristics(workflowID, nodeID, errStr)
	}

	var suggestion FixSuggestion
	if err := json.Unmarshal([]byte(content), &suggestion); err != nil {
		// Try to extract JSON if it's wrapped in markdown
		if start := strings.Index(content, "{"); start != -1 {
			if end := strings.LastIndex(content, "}"); end != -1 {
				json.Unmarshal([]byte(content[start:end+1]), &suggestion)
			}
		}
	}

	if suggestion.Explanation == "" {
		return s.analyzeErrorHeuristics(workflowID, nodeID, errStr)
	}

	return &suggestion, nil
}

func (s *SelfHealingService) analyzeErrorHeuristics(workflowID, nodeID, errStr string) (*FixSuggestion, error) {
	s.logger.Info("AI using heuristics for error analysis", "workflow_id", workflowID, "node_id", nodeID)

	if strings.Contains(strings.ToLower(errStr), "missing field") || strings.Contains(strings.ToLower(errStr), "key not found") {
		return &FixSuggestion{
			Explanation: "Heuristic: The downstream system expects a field that is missing in the source payload. I suggest adding a 'Set Fields' node or checking the mapping.",
			FixAction:   "update_mapping",
			Confidence:  0.85,
		}, nil
	}

	if strings.Contains(strings.ToLower(errStr), "timeout") || strings.Contains(strings.ToLower(errStr), "deadline exceeded") {
		return &FixSuggestion{
			Explanation: "Heuristic: The destination is responding slowly. Increasing the retry interval or adding a circuit breaker is recommended.",
			FixAction:   "change_config",
			ConfigPatch: map[string]any{"retry_interval": "5s", "cb_threshold": 3},
			Confidence:  0.92,
		}, nil
	}

	return &FixSuggestion{
		Explanation: "Heuristic: Structural mismatch detected. Please review the transformation logic.",
		FixAction:   "manual_review",
		Confidence:  0.40,
	}, nil
}

type CopilotResult struct {
	Language    string `json:"language"`
	Code        string `json:"code"`
	Explanation string `json:"explanation"`
}

type SchemaImpact struct {
	Breaking      bool     `json:"breaking"`
	ImpactedNodes []string `json:"impacted_nodes"`
	Suggestion    string   `json:"suggestion"`
}

func (s *SelfHealingService) AnalyzeSchemaChange(ctx context.Context, oldSchema, newSchema map[string]any, workflowID string) (*SchemaImpact, error) {
	s.logger.Info("AI analyzing schema evolution impact", "workflow_id", workflowID)

	if s.apiKey != "" || strings.HasPrefix(s.model, "ollama") {
		systemPrompt := "Analyze schema changes and identify breaking impacts. Return JSON with fields: breaking (bool), impacted_nodes ([]string), suggestion (string)."
		userPrompt := fmt.Sprintf("Workflow: %s\nOld Schema: %+v\nNew Schema: %+v", workflowID, oldSchema, newSchema)
		content, err := s.callLLM(ctx, systemPrompt, userPrompt)
		if err == nil {
			var impact SchemaImpact
			if err := json.Unmarshal([]byte(content), &impact); err == nil {
				return &impact, nil
			}
		}
	}

	// Heuristic Fallback
	impact := &SchemaImpact{
		Breaking:      false,
		ImpactedNodes: []string{},
		Suggestion:    "Schema updated successfully. No breaking changes detected (Heuristic).",
	}

	for k := range oldSchema {
		if _, ok := newSchema[k]; !ok {
			impact.Breaking = true
			impact.ImpactedNodes = append(impact.ImpactedNodes, "transformation_1", "sink_1")
			impact.Suggestion = "Field '" + k + "' was removed. This might break downstream nodes (Heuristic)."
		}
	}

	return impact, nil
}

func (s *SelfHealingService) GenerateLogic(ctx context.Context, prompt string) (*CopilotResult, error) {
	if s.apiKey != "" || strings.HasPrefix(s.model, "ollama") {
		systemPrompt := "You are a code generator for Hermod (Go/Lua). Return JSON with fields: language, code, explanation."
		content, err := s.callLLM(ctx, systemPrompt, prompt)
		if err == nil {
			var result CopilotResult
			if err := json.Unmarshal([]byte(content), &result); err == nil {
				return &result, nil
			}
		}
	}

	// Heuristic Fallback
	prompt = strings.ToLower(prompt)
	if strings.Contains(prompt, "currency") || strings.Contains(prompt, "usd") {
		return &CopilotResult{
			Language: "Lua",
			Code: `function transform(msg)
  local data = msg:data()
  if data.amount then
    data.amount_eur = data.amount * 0.92
    data.currency = "EUR"
  end
  msg:set_after(data)
  return msg
end`,
			Explanation: "Generated a Lua script to convert 'amount' from USD to EUR (Heuristic).",
		}, nil
	}

	return &CopilotResult{
		Language: "Lua",
		Code: `function transform(msg)
  local data = msg:data()
  msg:set_after(data)
  return msg
end`,
		Explanation: "Scaffolded a Lua transformation (Heuristic).",
	}, nil
}

func (s *SelfHealingService) GenerateWorkflow(ctx context.Context, prompt string) (any, error) {
	if s.apiKey != "" || strings.HasPrefix(s.model, "ollama") {
		systemPrompt := "Generate a Hermod workflow JSON from natural language description. Return valid workflow JSON."
		content, err := s.callLLM(ctx, systemPrompt, prompt)
		if err == nil {
			var wf any
			if err := json.Unmarshal([]byte(content), &wf); err == nil {
				return wf, nil
			}
		}
	}

	// Heuristic/Pattern Fallback (omitted for brevity, keeping simple version)
	return map[string]any{
		"name":  "AI Generated: " + prompt,
		"nodes": []any{},
		"edges": []any{},
	}, nil
}
