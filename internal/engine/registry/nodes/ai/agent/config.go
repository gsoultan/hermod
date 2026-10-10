package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gsoultan/hermod/internal/engine/registry/nodes/ai/agent/mcptool"
	"github.com/gsoultan/hermod/pkg/comm/transformer/core"
	"github.com/gsoultan/hermod/pkg/llm"
)

// Limits. Every one has a default and a ceiling the node cannot raise: a
// loop that talks to a paid model and can write to systems fails closed.
const (
	defaultMaxSteps       = 5
	hardMaxSteps          = 20
	defaultMaxTotalTokens = 50_000
	hardMaxTotalTokens    = 1_000_000
	defaultTimeout        = 2 * time.Minute
	hardTimeout           = 10 * time.Minute
)

// Tool kinds. A read kind is a lookup transformer run with the tool's fixed
// config; "sink" writes to a sink node of the same workflow; "mcp" calls one
// named tool of a remote MCP server.
const (
	kindSink = "sink"
	kindMCP  = "mcp"
)

// ReadKinds are the transformers a read tool may wrap. Each only fetches.
var ReadKinds = map[string]bool{"db_lookup": true, "api_lookup": true, "ai_retrieve": true}

// ToolKinds lists every tool kind the node accepts.
func ToolKinds() []string {
	return []string{"db_lookup", "api_lookup", "ai_retrieve", kindSink, kindMCP}
}

// toolName is what every provider accepts as a function name.
var toolName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// paramTypes are the JSON Schema types a parameter may declare.
var paramTypes = map[string]bool{"string": true, "number": true, "integer": true, "boolean": true}

type param struct {
	Name        string
	Type        string
	Description string
	Required    bool
}

// tool is one capability the node allows. Its name, description and schema
// come from the node's configuration only.
type tool struct {
	Name        string
	Description string
	Kind        string
	Params      []param
	// Config is the fixed transformer config of a read tool.
	Config map[string]any
	// NodeID is the sink node a sink tool writes to.
	NodeID string
	// Write marks a tool that changes something outside Hermod. A sink is
	// always one; a read kind can be marked one (an api_lookup that POSTs).
	Write bool
	// RequireApproval holds a write tool's call for a person. It defaults to
	// true and is only false when the node says so explicitly.
	RequireApproval bool
	// approvalOptOut is the node's explicit requireApproval: false. An mcp
	// tool learns whether it writes only from its server, so its
	// RequireApproval is settled then, from Write and this.
	approvalOptOut bool

	// Server is an mcp tool's server and Remote the one tool of it this tool
	// calls; the model never chooses either. endpoint is Server resolved for
	// the message being processed.
	Server   *mcptool.Server
	Remote   string
	endpoint mcptool.Endpoint
	// Schema is the remote tool's sanitized input schema. It is used, with
	// mcptool.BindArgs, when the node declares no parameters for the tool.
	Schema       map[string]any
	remoteSchema bool
}

type config struct {
	goal           string
	system         string
	targetField    string
	transcript     string
	maxSteps       int
	maxTotalTokens int64
	maxTokens      int
	temperature    *float64
	timeout        time.Duration
	tools          []tool
}

func parseConfig(raw map[string]any) (config, error) {
	c := config{
		goal:        strings.TrimSpace(core.GetConfigString(raw, "goal")),
		system:      strings.TrimSpace(core.GetConfigString(raw, "system")),
		targetField: core.GetConfigString(raw, "targetField"),
		transcript:  core.GetConfigString(raw, "transcriptField"),
	}
	if c.goal == "" {
		c.goal = strings.TrimSpace(core.GetConfigString(raw, "prompt"))
	}
	if c.goal == "" {
		return c, errors.New("a goal is required")
	}
	if c.targetField == "" {
		c.targetField = DefaultTargetField
	}
	if c.transcript == "" {
		c.transcript = DefaultTranscriptField
	}
	c.maxSteps = int(bounded(raw["maxSteps"], defaultMaxSteps, hardMaxSteps))
	c.maxTotalTokens = bounded(raw["maxTotalTokens"], defaultMaxTotalTokens, hardMaxTotalTokens)
	c.maxTokens = int(bounded(raw["maxTokens"], 0, hardMaxTotalTokens))
	if t, ok := number(raw["temperature"]); ok {
		c.temperature = &t
	}
	c.timeout = defaultTimeout
	if d, err := time.ParseDuration(core.GetConfigString(raw, "timeout")); err == nil && d > 0 {
		c.timeout = min(d, hardTimeout)
	}
	tools, err := parseTools(raw["tools"])
	if err != nil {
		return c, err
	}
	c.tools = tools
	return c, nil
}

// bounded reads a positive integer setting, falling back to def and never
// exceeding ceiling.
func bounded(v any, def, ceiling int64) int64 {
	f, ok := number(v)
	if !ok || f <= 0 {
		return def
	}
	return min(int64(f), ceiling)
}

func number(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		return f, err == nil
	}
	return 0, false
}

// parseTools reads the tools list, given as a list or as its JSON text.
func parseTools(raw any) ([]tool, error) {
	if s, ok := raw.(string); ok {
		if strings.TrimSpace(s) == "" {
			raw = nil
		} else if err := json.Unmarshal([]byte(s), &raw); err != nil {
			return nil, fmt.Errorf("tools is not valid JSON: %w", err)
		}
	}
	items, _ := raw.([]any)
	if len(items) == 0 {
		return nil, errors.New("at least one tool is required")
	}
	seen := map[string]bool{}
	tools := make([]tool, 0, len(items))
	for i, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("tool %d is not an object", i+1)
		}
		t, err := parseTool(m)
		if err != nil {
			return nil, fmt.Errorf("tool %d: %w", i+1, err)
		}
		if seen[t.Name] {
			return nil, fmt.Errorf("tool %q is listed twice", t.Name)
		}
		seen[t.Name] = true
		tools = append(tools, t)
	}
	return tools, nil
}

func parseTool(m map[string]any) (tool, error) {
	t := tool{
		Name:        strings.TrimSpace(core.GetConfigString(m, "name")),
		Description: core.GetConfigString(m, "description"),
		Kind:        strings.TrimSpace(core.GetConfigString(m, "kind")),
		NodeID:      strings.TrimSpace(core.GetConfigString(m, "nodeId")),
	}
	if !toolName.MatchString(t.Name) {
		return t, fmt.Errorf("name %q must be 1-64 letters, digits, _ or -", t.Name)
	}
	switch {
	case t.Kind == kindSink:
		if t.NodeID == "" {
			return t, fmt.Errorf("sink tool %q needs the sink node it writes to (nodeId)", t.Name)
		}
		t.Write = true
	case ReadKinds[t.Kind]:
		t.Config, _ = m["config"].(map[string]any)
		t.Write = m["write"] == true
	case t.Kind == kindMCP:
		if err := t.parseMCP(m); err != nil {
			return t, err
		}
	default:
		return t, fmt.Errorf("tool %q has kind %q; allowed are %s", t.Name, t.Kind, strings.Join(ToolKinds(), ", "))
	}
	t.approvalOptOut = m["requireApproval"] == false
	t.RequireApproval = t.Write && !t.approvalOptOut
	return t, t.parseParams(m["parameters"])
}

// parseParams reads the parameters the node declares for a tool.
func (t *tool) parseParams(raw any) error {
	params, _ := raw.([]any)
	seen := map[string]bool{}
	for _, raw := range params {
		pm, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("tool %q has a parameter that is not an object", t.Name)
		}
		p := param{
			Name:        strings.TrimSpace(core.GetConfigString(pm, "name")),
			Type:        strings.TrimSpace(core.GetConfigString(pm, "type")),
			Description: core.GetConfigString(pm, "description"),
			Required:    pm["required"] == true,
		}
		if p.Type == "" {
			p.Type = "string"
		}
		if !toolName.MatchString(p.Name) || seen[p.Name] {
			return fmt.Errorf("tool %q has a missing, invalid or repeated parameter name %q", t.Name, p.Name)
		}
		if !paramTypes[p.Type] {
			return fmt.Errorf("tool %q parameter %q has type %q; allowed are string, number, integer, boolean", t.Name, p.Name, p.Type)
		}
		seen[p.Name] = true
		t.Params = append(t.Params, p)
	}
	return nil
}

// parseMCP reads an mcp tool's server and remote tool name. Whether the tool
// writes is only known once its server describes it (see run.prepare); until
// then only the node's own write flag counts.
func (t *tool) parseMCP(m map[string]any) error {
	srv, err := mcptool.ParseServer(m["server"])
	if err != nil {
		return fmt.Errorf("mcp tool %q: %w", t.Name, err)
	}
	t.Server = &srv
	t.Remote = strings.TrimSpace(core.GetConfigString(m, "tool"))
	if t.Remote == "" {
		return fmt.Errorf("mcp tool %q needs the name of the remote tool it calls (tool)", t.Name)
	}
	t.Write = m["write"] == true
	t.remoteSchema = m["parameters"] == nil
	return nil
}

// useRemote applies what the server says about an mcp tool. A tool the
// server does not mark read-only is a write tool, held for approval unless
// the node opted out. The node's own description, when it has one, wins
// over the server's.
func (t *tool) useRemote(ep mcptool.Endpoint, r mcptool.Remote) {
	t.endpoint = ep
	if !r.ReadOnly {
		t.Write = true
	}
	t.RequireApproval = t.Write && !t.approvalOptOut
	if t.remoteSchema {
		t.Schema = r.Schema
	}
	if strings.TrimSpace(t.Description) == "" {
		t.Description = r.Description
	}
}

// spec is the tool as the model sees it.
func (t tool) spec() llm.ToolSpec {
	props := make(map[string]any, len(t.Params))
	required := []any{}
	for _, p := range t.Params {
		prop := map[string]any{"type": p.Type}
		if p.Description != "" {
			prop["description"] = p.Description
		}
		props[p.Name] = prop
		if p.Required {
			required = append(required, p.Name)
		}
	}
	desc := t.Description
	if t.RequireApproval {
		desc = strings.TrimSpace(desc + " (A person must approve each call before it runs.)")
	}
	if t.Schema != nil {
		return llm.ToolSpec{Name: t.Name, Description: desc, Schema: t.Schema}
	}
	return llm.ToolSpec{
		Name:        t.Name,
		Description: desc,
		Schema: map[string]any{
			"type":                 "object",
			"properties":           props,
			"required":             required,
			"additionalProperties": false,
		},
	}
}

// bindArgs keeps the declared parameters of a call and checks their types.
// Anything the model sends beyond them is dropped: it cannot reach the tool.
func (t tool) bindArgs(input json.RawMessage) (map[string]any, error) {
	var in map[string]any
	if len(input) > 0 && string(input) != "null" {
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, fmt.Errorf("arguments are not a JSON object: %w", err)
		}
	}
	if t.Schema != nil {
		return mcptool.BindArgs(t.Schema, in)
	}
	out := make(map[string]any, len(t.Params))
	for _, p := range t.Params {
		v, ok := in[p.Name]
		if !ok || v == nil {
			if p.Required {
				return nil, fmt.Errorf("argument %q is required", p.Name)
			}
			continue
		}
		if !hasType(v, p.Type) {
			return nil, fmt.Errorf("argument %q must be a %s", p.Name, p.Type)
		}
		out[p.Name] = v
	}
	return out, nil
}

func hasType(v any, typ string) bool {
	switch typ {
	case "string":
		_, ok := v.(string)
		return ok
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "number":
		_, ok := v.(float64)
		return ok
	case "integer":
		f, ok := v.(float64)
		return ok && f == float64(int64(f))
	}
	return false
}
