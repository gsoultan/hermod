package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/storage"
)

type FixSuggestion struct {
	Explanation string  `json:"explanation"`
	Description string  `json:"description,omitempty"`
	FixAction   string  `json:"fix_action"` // "update_mapping", "add_node", "change_config"
	ConfigPatch any     `json:"config_patch,omitempty"`
	Confidence  float64 `json:"confidence"`
	AutoFixable bool    `json:"auto_fixable"`
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
		return "", errors.New("AI service not configured: OPENAI_API_KEY is missing")
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
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
		return "", errors.New("AI service returned no choices")
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

	errLower := strings.ToLower(errStr)

	if strings.Contains(errLower, "failed to get source for lookup") {
		return &FixSuggestion{
			Explanation: "Architectural Mismatch: The DB Lookup node cannot resolve its linked data source. This usually indicates a broken reference in the workflow definition or that the target source has been deleted.",
			Description: "1. Navigate to the Source Registry and confirm the source exists.\n2. In the DB Lookup node, verify 'sourceId' matches the actual Source ID.\n3. Note: DB Lookups on high-throughput replication like Autocount must use non-CDC sources for the lookup itself to avoid logical replication overhead.",
			FixAction:   "change_config",
			Confidence:  0.99,
		}, nil
	}

	if strings.Contains(errLower, "db_lookup requires a non-cdc source") {
		return &FixSuggestion{
			Explanation: "Performance Constraint: Logical replication (CDC) sources cannot be used for direct point-lookups due to consistency and throughput limitations.",
			Description: "Create a separate 'Snapshot' or 'Standard' source pointing to the same database but with CDC disabled. Use this new source ID for your DB Lookup node. This ensures optimal lookup speed without interrupting the main replication stream.",
			FixAction:   "change_config",
			Confidence:  0.95,
		}, nil
	}

	if strings.Contains(errLower, "missing field") || strings.Contains(errLower, "key not found") {
		return &FixSuggestion{
			Explanation: "Data integrity issue: The downstream node expects a specific field that is missing from the incoming message payload.",
			Description: "Review your mapping or transformation logic. You might need to add a 'Set Fields' node or update a 'Mapping' node to ensure the required field is present. If the field is optional, consider using a default value.",
			FixAction:   "update_mapping",
			Confidence:  0.85,
		}, nil
	}

	if strings.Contains(errLower, "timeout") || strings.Contains(errLower, "deadline exceeded") || strings.Contains(errLower, "connection refused") {
		return &FixSuggestion{
			Explanation: "Network or Resource bottleneck: The destination system is unreachable or responding too slowly to process the request within the allotted time.",
			Description: "Try increasing the 'timeout' or 'retry_interval' in the node configuration. For high-volume workflows, consider adding a 'Buffer' node before the sink to handle traffic spikes.",
			FixAction:   "change_config",
			ConfigPatch: map[string]any{"retry_interval": "5s", "cb_threshold": 3},
			Confidence:  0.92,
		}, nil
	}

	if strings.Contains(errLower, "auth") || strings.Contains(errLower, "permission denied") || strings.Contains(errLower, "401") || strings.Contains(errLower, "403") {
		return &FixSuggestion{
			Explanation: "Access denied: The credentials provided for this source or sink are either invalid or do not have sufficient permissions.",
			Description: "Verify the username, password, or API token in the source/sink configuration. Ensure the account has the necessary read/write permissions for the target resources.",
			FixAction:   "change_config",
			Confidence:  0.95,
		}, nil
	}

	if strings.Contains(errLower, "batch_size") || strings.Contains(errLower, "too many requests") {
		return &FixSuggestion{
			Explanation: "Throughput limitation: The target system cannot handle the current volume of requests or the batch size is too large for a single transaction.",
			Description: "Adjust the 'batch_size' in the sink configuration. For high-throughput ETL like Autocount, a batch size of 500-1000 is usually optimal. If you see 'too many requests', consider adding a 'Delay' or 'Buffer' node.",
			FixAction:   "change_config",
			Confidence:  0.90,
		}, nil
	}

	return &FixSuggestion{
		Explanation: "Heuristic: Structural mismatch detected. The input data format might have changed or is incompatible with the current transformation logic.",
		Description: "Review the 'Sample Data' and compare it with your transformation script (Lua/Go) or mapping rules. Ensure that you are correctly accessing fields based on the input structure.",
		FixAction:   "manual_review",
		Confidence:  0.40,
	}, nil
}

type CopilotResult struct {
	Language    string `json:"language"`
	Code        string `json:"code"`
	Explanation string `json:"explanation"`
}

type PerformanceRecommendation struct {
	WorkflowID  string   `json:"workflow_id"`
	Bottlenecks []string `json:"bottlenecks"`
	Suggestions []string `json:"suggestions"`
	Score       float64  `json:"score"` // 0-100
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

// SuggestMapping analyzes source data and a target schema to suggest field mappings.
func (s *SelfHealingService) SuggestMapping(ctx context.Context, sourceData map[string]any, targetSchema map[string]any) (string, error) {
	if s.apiKey != "" || strings.HasPrefix(s.model, "ollama") {
		systemPrompt := "Compare source data and target schema. Suggest field mappings. Return a human-readable list or JSON mapping."
		userPrompt := fmt.Sprintf("Source Data: %+v\nTarget Schema: %+v", sourceData, targetSchema)
		content, err := s.callLLM(ctx, systemPrompt, userPrompt)
		if err == nil {
			return content, nil
		}
	}

	// Heuristic Fallback
	var suggestions []string
	for k := range sourceData {
		for tk := range targetSchema {
			if strings.EqualFold(k, tk) && k != tk {
				suggestions = append(suggestions, fmt.Sprintf("Match found: '%s' -> '%s'", k, tk))
			}
		}
	}
	if len(suggestions) > 0 {
		return "Suggested Mappings (Heuristic):\n" + strings.Join(suggestions, "\n"), nil
	}
	return "No clear mappings found (Heuristic). Please review manually.", nil
}

// AnalyzeWorkflow provides holistic performance and configuration recommendations for a workflow.
func (s *SelfHealingService) AnalyzeWorkflow(ctx context.Context, wf storage.Workflow) (*PerformanceRecommendation, error) {
	if s.apiKey != "" || strings.HasPrefix(s.model, "ollama") {
		s.logger.Info("AI analyzing workflow performance", "workflow_id", wf.ID)

		systemPrompt := "You are a performance tuning expert for data pipelines. Analyze the workflow configuration and metrics. Identify bottlenecks and suggest improvements. Return JSON with fields: workflow_id, bottlenecks ([]string), suggestions ([]string), score (float64, 0-100)."
		userPrompt := fmt.Sprintf("Workflow: %+v", wf)

		content, err := s.callLLM(ctx, systemPrompt, userPrompt)
		if err == nil {
			var rec PerformanceRecommendation
			if err := json.Unmarshal([]byte(content), &rec); err == nil {
				return &rec, nil
			}
		}
	}

	// Heuristic Fallback
	rec := &PerformanceRecommendation{
		WorkflowID:  wf.ID,
		Bottlenecks: []string{},
		Suggestions: []string{},
		Score:       100.0,
	}

	// Simple heuristic rules
	if wf.TotalErrors > 0 && wf.TotalProcessed > 0 {
		errorRate := float64(wf.TotalErrors) / float64(wf.TotalProcessed)
		if errorRate > 0.1 {
			rec.Bottlenecks = append(rec.Bottlenecks, "High error rate detected")
			rec.Suggestions = append(rec.Suggestions, "Check transformation logic and source data quality")
			rec.Score -= 20
		}
	}

	if wf.TotalLag > 1000 {
		rec.Bottlenecks = append(rec.Bottlenecks, "Significant message lag")
		rec.Suggestions = append(rec.Suggestions, "Consider increasing worker count or optimizing sink performance")
		rec.Score -= 15
	}

	hasBuffer := false
	for _, n := range wf.Nodes {
		if n.Type == "buffer" || n.Type == "queue" {
			hasBuffer = true
			break
		}
	}

	if !hasBuffer && wf.ThroughputRequest > 500 {
		rec.Bottlenecks = append(rec.Bottlenecks, "Missing buffer for high-throughput workflow")
		rec.Suggestions = append(rec.Suggestions, "Add a buffer or queue node to handle bursts and prevent source pressure.")
		rec.Score -= 10
	}

	// Advanced Heuristics
	remoteSinksCount := 0
	sinksWithoutCB := 0
	transformNodesCount := 0
	hasSequentialSink := false

	for _, n := range wf.Nodes {
		if n.Type == "sink" {
			remoteSinksCount++
			if n.Config["cb_threshold"] == nil || n.Config["cb_threshold"] == 0 {
				sinksWithoutCB++
			}
			if seq, ok := n.Config["sequential"].(bool); ok && seq {
				hasSequentialSink = true
			}
		}
		if n.Type == "transformation" || n.Type == "mapping" || n.Type == "script" {
			transformNodesCount++
		}
	}

	if sinksWithoutCB > 0 {
		rec.Bottlenecks = append(rec.Bottlenecks, fmt.Sprintf("%d sink(s) missing circuit breaker", sinksWithoutCB))
		rec.Suggestions = append(rec.Suggestions, "Enable circuit breakers on all external sinks to prevent cascading failures.")
		rec.Score -= float64(sinksWithoutCB * 5)
	}

	if transformNodesCount >= 5 {
		rec.Bottlenecks = append(rec.Bottlenecks, "Large number of transformation nodes")
		rec.Suggestions = append(rec.Suggestions, "Consider consolidating multiple transformation nodes into a single Lua script node for better performance.")
		rec.Score -= 10
	}

	// Caching check for DB Lookups
	lookupNodesWithoutCache := 0
	for _, n := range wf.Nodes {
		if n.Type == "db_lookup" || (n.Type == "transformation" && n.Config["type"] == "db_lookup") {
			if ttl, ok := n.Config["ttl"].(string); !ok || ttl == "" || ttl == "0s" {
				lookupNodesWithoutCache++
			}
		}
	}

	if lookupNodesWithoutCache > 0 {
		rec.Bottlenecks = append(rec.Bottlenecks, fmt.Sprintf("%d DB Lookup node(s) without caching", lookupNodesWithoutCache))
		rec.Suggestions = append(rec.Suggestions, "Enable caching (TTL) on DB Lookup nodes to reduce database load and improve transformation speed.")
		rec.Score -= float64(lookupNodesWithoutCache * 8)
	}

	if hasSequentialSink && wf.ThroughputRequest > 100 {
		rec.Bottlenecks = append(rec.Bottlenecks, "Sequential sink in high-throughput workflow")
		rec.Suggestions = append(rec.Suggestions, "Disable 'sequential' mode on sinks if message order per table is not strictly required, to allow parallel processing.")
		rec.Score -= 15
	}

	if strings.Contains(strings.ToLower(wf.Name), "autocount") {
		// Specific recommendations for Autocount ETL (typical large-scale replication)
		rec.Suggestions = append(rec.Suggestions, "For Autocount ETL, ensure source connection uses PgBouncer (non-CDC) or direct session-mode (CDC) for optimal reliability.")
		rec.Suggestions = append(rec.Suggestions, "Use 'db_lookup' with at least 1m TTL for master data tables (Item, Account, Debtor) to maximize throughput.")
		rec.Suggestions = append(rec.Suggestions, "Enable 'batch_size' >= 500 on MS SQL/Postgres sinks to optimize IOPS during large replication batches.")
	}

	if len(rec.Bottlenecks) == 0 {
		rec.Suggestions = append(rec.Suggestions, "Workflow looks healthy and well-configured.")
	}

	return rec, nil
}
