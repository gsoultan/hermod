package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/gsoultan/hermod/internal/storage"
)

// ModelSource is the part of the model registry the model tools use. Predict
// is the registry's one inference path, so a model called over MCP is held to
// the same quotas and metrics as every other caller's.
type ModelSource interface {
	ListModels(ctx context.Context, vhost string) ([]storage.MLModel, error)
	Predict(ctx context.Context, vhost, name string, rows []map[string]any) ([]map[string]any, error)
}

// ErrModelNotFound is returned for a model tool the caller may not use.
var ErrModelNotFound = errors.New("no MCP-exposed model by that name")

// modelToolPrefix starts every model tool's name.
const modelToolPrefix = "predict_"

// maxToolName is the longest tool name MCP allows.
const maxToolName = 128

// ModelTool is one model a vhost exposed, as an MCP tool. It is built per
// request for the caller, like the server it is added to.
type ModelTool struct {
	// Name is predict_<model>, or predict_<model>__<vhost> when the caller
	// can reach two vhosts exposing a model of that name.
	Name        string
	VHost       string
	Model       string
	Description string
	// InputSchema has one property per feature, typed where the type is
	// known, all required; a model that declares no features takes any
	// object as its one row.
	InputSchema map[string]any

	features []string
	types    map[string]string
}

// PredictResult is what a model tool answers.
type PredictResult struct {
	Model      string         `json:"model"`
	VHost      string         `json:"vhost"`
	Prediction map[string]any `json:"prediction"`
}

// ModelTools returns a tool for every model exposed to MCP in the vhosts
// given that the caller may use. A vhost the caller may not use is skipped
// whatever the list says.
func (s *Service) ModelTools(ctx context.Context, c Caller, vhosts []string) ([]ModelTool, error) {
	if s.Models == nil {
		return nil, nil
	}
	vhosts = slices.Clone(vhosts)
	slices.Sort(vhosts)
	vhosts = slices.Compact(vhosts)

	var tools []ModelTool
	count := map[string]int{}
	for _, vhost := range vhosts {
		if vhost == "" || c.MayAccess == nil || !c.MayAccess(vhost) {
			continue
		}
		models, err := s.Models.ListModels(ctx, vhost)
		if err != nil {
			return nil, fmt.Errorf("listing the models of vhost %q: %w", vhost, err)
		}
		for _, m := range models {
			if !m.MCPExposed {
				continue
			}
			tools = append(tools, modelTool(m))
			count[m.Name]++
		}
	}

	// A name two vhosts share is told apart by the vhost; anything that
	// still collides is left out rather than shadowing another tool.
	seen := map[string]bool{}
	out := tools[:0]
	for _, t := range tools {
		if count[t.Model] > 1 {
			t.Name = clipName(t.Name + "__" + toolSafe(t.VHost))
		}
		if seen[t.Name] {
			continue
		}
		seen[t.Name] = true
		out = append(out, t)
	}
	return out, nil
}

func modelTool(m storage.MLModel) ModelTool {
	desc := fmt.Sprintf("Runs Hermod model %q of vhost %q on one record and returns its prediction.", m.Name, m.VHost)
	if d := strings.TrimSpace(m.Description); d != "" {
		desc = d + " " + desc
	}
	schema := map[string]any{"type": "object"}
	if len(m.Features) > 0 {
		props := make(map[string]any, len(m.Features))
		required := make([]any, 0, len(m.Features))
		for _, f := range m.Features {
			prop := map[string]any{}
			if t := jsonType(m.FeatureTypes[f]); t != "" {
				prop["type"] = t
			}
			props[f] = prop
			required = append(required, f)
		}
		schema["properties"] = props
		schema["required"] = required
		schema["additionalProperties"] = false
	}
	return ModelTool{
		Name: clipName(modelToolPrefix + m.Name), VHost: m.VHost, Model: m.Name, Description: desc,
		InputSchema: schema, features: m.Features, types: m.FeatureTypes,
	}
}

// jsonType is the JSON Schema type of a dataset column type.
func jsonType(t string) string {
	switch t {
	case "number":
		return "number"
	case "string":
		return "string"
	case "bool":
		return "boolean"
	}
	return ""
}

// toolSafe replaces what a tool name may not hold.
func toolSafe(s string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' {
			return r
		}
		return '_'
	}, s)
}

func clipName(n string) string {
	if len(n) > maxToolName {
		return n[:maxToolName]
	}
	return n
}

// PredictTool runs one call of a model tool: the arguments, bound to the
// model's features, are its one row.
func (s *Service) PredictTool(ctx context.Context, c Caller, t ModelTool, args json.RawMessage) (PredictResult, error) {
	if s.Models == nil || c.MayAccess == nil || !c.MayAccess(t.VHost) {
		return PredictResult{}, ErrModelNotFound
	}
	row, err := t.bind(args)
	if err != nil {
		return PredictResult{}, err
	}
	preds, err := s.Models.Predict(ctx, t.VHost, t.Model, []map[string]any{row})
	if err != nil {
		return PredictResult{}, err
	}
	if len(preds) != 1 {
		return PredictResult{}, fmt.Errorf("model %q returned %d predictions for one record", t.Model, len(preds))
	}
	return PredictResult{Model: t.Model, VHost: t.VHost, Prediction: preds[0]}, nil
}

// bind checks the arguments against the model's features: every feature
// present, of its type where that is known, and nothing else.
func (t ModelTool) bind(args json.RawMessage) (map[string]any, error) {
	in := map[string]any{}
	if len(args) > 0 && string(args) != "null" {
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, errors.New("the arguments must be a JSON object of the model's features")
		}
	}
	if len(t.features) == 0 {
		return in, nil
	}
	for k := range in {
		if !slices.Contains(t.features, k) {
			return nil, fmt.Errorf("%q is not a feature of this model; its features are %s", k, strings.Join(t.features, ", "))
		}
	}
	for _, f := range t.features {
		v, ok := in[f]
		if !ok || v == nil {
			return nil, fmt.Errorf("feature %q is required", f)
		}
		if !ofType(v, t.types[f]) {
			return nil, fmt.Errorf("feature %q must be a %s", f, jsonType(t.types[f]))
		}
	}
	return in, nil
}

func ofType(v any, typ string) bool {
	switch typ {
	case "number":
		_, ok := v.(float64)
		return ok
	case "string":
		_, ok := v.(string)
		return ok
	case "bool":
		_, ok := v.(bool)
		return ok
	}
	return true
}
