package mcpserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/gsoultan/hermod/internal/mcpserver"
	"github.com/gsoultan/hermod/internal/storage"
)

// fakeModels is a model registry in memory whose models echo their input.
type fakeModels struct {
	models []storage.MLModel
	calls  []string
	rows   []map[string]any
}

func (f *fakeModels) ListModels(_ context.Context, vhost string) ([]storage.MLModel, error) {
	var out []storage.MLModel
	for _, m := range f.models {
		if m.VHost == vhost {
			out = append(out, m)
		}
	}
	return out, nil
}

func (f *fakeModels) Predict(_ context.Context, vhost, name string, rows []map[string]any) ([]map[string]any, error) {
	f.calls = append(f.calls, vhost+"/"+name)
	f.rows = append(f.rows, rows...)
	return []map[string]any{{"score": 0.9}}, nil
}

func fraud(vhost string, exposed bool) storage.MLModel {
	return storage.MLModel{
		VHost: vhost, Name: "fraud", Description: "Scores an order", MCPExposed: exposed,
		Features: []string{"amount", "country", "first_order"}, FeatureTypes: map[string]string{"amount": "number", "first_order": "bool"},
	}
}

func toolNames(ts []mcpserver.ModelTool) map[string]mcpserver.ModelTool {
	out := map[string]mcpserver.ModelTool{}
	for _, t := range ts {
		out[t.Name] = t
	}
	return out
}

func TestModelToolsAreTheExposedModelsOfTheCallersVHosts(t *testing.T) {
	hidden := fraud("tenant-a", false)
	hidden.Name = "payroll"
	models := &fakeModels{models: []storage.MLModel{fraud("tenant-a", true), hidden, fraud("tenant-b", true)}}
	svc := &mcpserver.Service{Models: models}

	tools, err := svc.ModelTools(t.Context(), editorOf("tenant-a"), []string{"tenant-a", "tenant-b"})
	if err != nil {
		t.Fatalf("ModelTools: %v", err)
	}
	byName := toolNames(tools)
	if len(tools) != 1 {
		t.Fatalf("tools = %v, want only predict_fraud of tenant-a", byName)
	}
	tool, ok := byName["predict_fraud"]
	if !ok || tool.VHost != "tenant-a" || tool.Model != "fraud" {
		t.Fatalf("tool = %+v", tool)
	}

	schema := tool.InputSchema
	props, _ := schema["properties"].(map[string]any)
	if schema["type"] != "object" || schema["additionalProperties"] != false || len(props) != 3 {
		t.Fatalf("schema = %v", schema)
	}
	if props["amount"].(map[string]any)["type"] != "number" || props["first_order"].(map[string]any)["type"] != "boolean" {
		t.Errorf("the known types are not in the schema: %v", props)
	}
	if _, typed := props["country"].(map[string]any)["type"]; typed {
		t.Errorf("a feature of unknown type was given one: %v", props["country"])
	}
	if req, _ := schema["required"].([]any); len(req) != 3 {
		t.Errorf("required = %v, want every feature", schema["required"])
	}
}

func TestTheSameModelNameInTwoVHostsGetsTwoNamedTools(t *testing.T) {
	models := &fakeModels{models: []storage.MLModel{fraud("tenant-a", true), fraud("tenant b", true)}}
	svc := &mcpserver.Service{Models: models}
	all := mcpserver.Caller{MayAccess: func(string) bool { return true }}

	tools, err := svc.ModelTools(t.Context(), all, []string{"tenant b", "tenant-a", "tenant-a"})
	if err != nil {
		t.Fatal(err)
	}
	byName := toolNames(tools)
	if len(tools) != 2 || byName["predict_fraud__tenant-a"].VHost != "tenant-a" || byName["predict_fraud__tenant_b"].VHost != "tenant b" {
		t.Fatalf("tools = %v", byName)
	}
}

func TestPredictBindsTheCallToTheModelsFeatures(t *testing.T) {
	models := &fakeModels{models: []storage.MLModel{fraud("tenant-a", true)}}
	svc := &mcpserver.Service{Models: models}
	caller := editorOf("tenant-a")
	tools, _ := svc.ModelTools(t.Context(), caller, []string{"tenant-a"})
	tool := tools[0]

	res, err := svc.PredictTool(t.Context(), caller, tool, json.RawMessage(`{"amount": 12.5, "country": "ID", "first_order": true}`))
	if err != nil {
		t.Fatalf("PredictTool: %v", err)
	}
	if res.Model != "fraud" || res.VHost != "tenant-a" || res.Prediction["score"] != 0.9 {
		t.Errorf("result = %+v", res)
	}
	if len(models.calls) != 1 || models.calls[0] != "tenant-a/fraud" || models.rows[0]["amount"] != 12.5 {
		t.Errorf("called %v with %v", models.calls, models.rows)
	}

	for name, args := range map[string]string{
		"missing feature": `{"amount": 1, "country": "ID"}`,
		"wrong type":      `{"amount": "lots", "country": "ID", "first_order": true}`,
		"unknown field":   `{"amount": 1, "country": "ID", "first_order": true, "model": "payroll"}`,
		"not an object":   `[1, 2]`,
	} {
		if _, err := svc.PredictTool(t.Context(), caller, tool, json.RawMessage(args)); err == nil {
			t.Errorf("%s: the call was not refused", name)
		}
	}
	if len(models.calls) != 1 {
		t.Errorf("a refused call reached the model: %v", models.calls)
	}

	stranger := editorOf("tenant-b")
	if _, err := svc.PredictTool(t.Context(), stranger, tool, json.RawMessage(`{}`)); !errors.Is(err, mcpserver.ErrModelNotFound) {
		t.Errorf("a caller without the vhost: err = %v, want ErrModelNotFound", err)
	}
}

func TestAModelWithoutDeclaredFeaturesTakesAnyObject(t *testing.T) {
	m := storage.MLModel{VHost: "tenant-a", Name: "free", MCPExposed: true}
	models := &fakeModels{models: []storage.MLModel{m}}
	svc := &mcpserver.Service{Models: models}
	tools, _ := svc.ModelTools(t.Context(), editorOf("tenant-a"), []string{"tenant-a"})
	if len(tools) != 1 || tools[0].InputSchema["type"] != "object" || tools[0].InputSchema["properties"] != nil {
		t.Fatalf("tools = %+v", tools)
	}
	if _, err := svc.PredictTool(t.Context(), editorOf("tenant-a"), tools[0], json.RawMessage(`{"x": 1}`)); err != nil {
		t.Fatalf("PredictTool: %v", err)
	}
	if models.rows[0]["x"] != 1.0 {
		t.Errorf("row = %v", models.rows[0])
	}
}

func TestNoModelSourceMeansNoModelTools(t *testing.T) {
	tools, err := (&mcpserver.Service{}).ModelTools(t.Context(), editorOf("tenant-a"), []string{"tenant-a"})
	if err != nil || len(tools) != 0 {
		t.Errorf("tools = %v, %v", tools, err)
	}
}
