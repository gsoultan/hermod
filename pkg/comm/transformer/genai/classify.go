package genai

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/transformer/core"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
	"github.com/gsoultan/hermod/pkg/llm"
)

const (
	// UnsureLabel is the branch taken when the model's confidence is below
	// the node's threshold or it answers with a label it was not offered.
	UnsureLabel = "unsure"
	// DefaultLabelField and DefaultConfidenceField are where Classify writes.
	DefaultLabelField      = "ai_label"
	DefaultConfidenceField = "ai_confidence"
)

// Classification is the model's choice.
type Classification struct {
	Label      string
	Confidence float64
	Reason     string
	Usage      llm.Usage
}

type labelDef struct {
	name, description string
}

// Classify asks the model to put msg into one of the node's labels and
// writes the label and confidence into the message. It is the core of the
// ai_classify node, which routes on the returned label.
//
// Config: labels (a list of {label, description} or comma-separated names),
// instructions, threshold (0-1), targetField, confidenceField, inputFields,
// maskFields, maskPII, and the connection keys read by ProviderFor.
func Classify(ctx context.Context, msg hermod.Message, config map[string]any) (Classification, error) {
	labels := parseLabels(config["labels"])
	if len(labels) == 0 {
		return Classification{}, errors.New("ai_classify: at least one label is required")
	}
	system, schema := classifyRequest(labels, core.GetConfigString(config, "instructions"))
	resp, err := chat(ctx, config, msg, llm.ChatRequest{
		System:         system,
		Messages:       []llm.Message{{Role: llm.RoleUser, Text: "Input:\n" + inputJSON(msg, config)}},
		ResponseSchema: schema,
		MaxTokens:      1024,
	})
	if err != nil {
		return Classification{}, fmt.Errorf("ai_classify: %w", err)
	}
	obj, err := parseJSONObject(resp.Text)
	if err != nil {
		return Classification{}, fmt.Errorf("ai_classify: %w", err)
	}
	c := Classification{Usage: resp.Usage}
	c.Label, _ = obj["label"].(string)
	c.Confidence, _ = obj["confidence"].(float64)
	c.Reason, _ = obj["reason"].(string)

	threshold, _ := strconv.ParseFloat(core.GetConfigString(config, "threshold"), 64)
	if !offered(labels, c.Label) || c.Confidence < threshold {
		c.Label = UnsureLabel
	}

	writeClassification(msg, config, c)
	writeUsage(msg, config, resp)
	return c, nil
}

func classifyRequest(labels []labelDef, instr string) (string, map[string]any) {
	names := make([]any, 0, len(labels))
	var guide strings.Builder
	guide.WriteString("Classify the input into exactly one of these labels.\n")
	for _, l := range labels {
		names = append(names, l.name)
		guide.WriteString("- " + l.name)
		if l.description != "" {
			guide.WriteString(": " + l.description)
		}
		guide.WriteString("\n")
	}
	if instr = strings.TrimSpace(instr); instr != "" {
		guide.WriteString("\n" + instr + "\n")
	}
	guide.WriteString("\nGive your confidence from 0 to 1 and a one-sentence reason.")

	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"label":      map[string]any{"type": "string", "enum": names},
			"confidence": map[string]any{"type": "number"},
			"reason":     map[string]any{"type": "string"},
		},
		"required":             []any{"label", "confidence", "reason"},
		"additionalProperties": false,
	}
	return guide.String(), schema
}

func writeClassification(msg hermod.Message, config map[string]any, c Classification) {
	field := core.GetConfigString(config, "targetField")
	if field == "" {
		field = DefaultLabelField
	}
	confField := core.GetConfigString(config, "confidenceField")
	if confField == "" {
		confField = DefaultConfidenceField
	}
	msg.SetData(field, c.Label)
	msg.SetData(confField, c.Confidence)
}

func offered(labels []labelDef, name string) bool {
	for _, l := range labels {
		if l.name == name {
			return true
		}
	}
	return false
}

func parseLabels(raw any) []labelDef {
	var out []labelDef
	switch v := raw.(type) {
	case string:
		if strings.HasPrefix(strings.TrimSpace(v), "[") {
			return parseLabels(evaluator.ParseObjectList(v))
		}
		for part := range strings.SplitSeq(v, ",") {
			if p := strings.TrimSpace(part); p != "" {
				out = append(out, labelDef{name: p})
			}
		}
	case []any:
		for _, item := range v {
			if l, ok := parseLabel(item); ok {
				out = append(out, l)
			}
		}
	case []map[string]any:
		for _, it := range v {
			out = append(out, parseLabels([]any{it})...)
		}
	}
	return out
}

func parseLabel(item any) (labelDef, bool) {
	switch it := item.(type) {
	case string:
		name := strings.TrimSpace(it)
		return labelDef{name: name}, name != ""
	case map[string]any:
		name, _ := it["label"].(string)
		desc, _ := it["description"].(string)
		name = strings.TrimSpace(name)
		return labelDef{name: name, description: desc}, name != ""
	}
	return labelDef{}, false
}
