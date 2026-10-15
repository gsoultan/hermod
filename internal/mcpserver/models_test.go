// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package mcpserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/gsoultan/hermod/internal/mcpserver"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/recommend"
)

// fakeModels is a model registry in memory whose models echo their input.
type fakeModels struct {
	models []storage.MLModel
	calls  []string
	rows   []map[string]any
	recs   []mcpserver.RecommendArgs
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

// Recommend answers the popular items with the request it was given recorded.
func (f *fakeModels) Recommend(_ context.Context, vhost, name string, req mcpserver.RecommendArgs) ([]recommend.Item, string, error) {
	f.calls = append(f.calls, vhost+"/"+name)
	f.recs = append(f.recs, req)
	return []recommend.Item{{Item: "sku-1", Score: 3, Reason: recommend.ReasonPopular}}, "rec_1", nil
}

// RecommendScenario answers one result for the salesman, recording the call.
func (f *fakeModels) RecommendScenario(_ context.Context, vhost, name string, req mcpserver.RecommendArgs) (mcpserver.ScenarioAnswer, error) {
	f.calls = append(f.calls, vhost+"/"+name)
	f.recs = append(f.recs, req)
	return mcpserver.ScenarioAnswer{RecommendationID: "rec_2", Results: []recommend.Result{{For: "R1",
		Items: []recommend.Pick{{ID: "C1", Score: 1, Rank: 1, Reasons: []recommend.Reason{{Code: "overdue", Text: "Overdue"}}}}}}}, nil
}

func recs(vhost string) storage.MLModel {
	return storage.MLModel{VHost: vhost, Name: "recs", MCPExposed: true, Task: storage.MLTaskRecommendation,
		Description: "Suggests products"}
}

// A recommendation model is a recommend_<model> tool taking a user or an
// item, not its (empty) features.
func TestARecommenderIsARecommendTool(t *testing.T) {
	models := &fakeModels{models: []storage.MLModel{recs("tenant-a"), fraud("tenant-a", true)}}
	svc := &mcpserver.Service{Models: models}
	caller := editorOf("tenant-a")
	tools, err := svc.ModelTools(t.Context(), caller, []string{"tenant-a"})
	if err != nil {
		t.Fatal(err)
	}
	byName := toolNames(tools)
	tool, ok := byName["recommend_recs"]
	if !ok || !tool.Recommend || len(tools) != 2 || byName["predict_fraud"].Recommend {
		t.Fatalf("tools = %v", byName)
	}
	props, _ := tool.InputSchema["properties"].(map[string]any)
	for _, p := range []string{"user", "item", "for", "period", "within_days", "rules", "territory", "k", "exclude_seen"} {
		if _, ok := props[p]; !ok {
			t.Errorf("schema has no %q: %v", p, tool.InputSchema)
		}
	}
	if tool.InputSchema["additionalProperties"] != false {
		t.Errorf("schema = %v", tool.InputSchema)
	}

	res, err := svc.RecommendTool(t.Context(), caller, tool, json.RawMessage(`{"user": "1000", "k": 5, "exclude_seen": false}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.Model != "recs" || res.VHost != "tenant-a" || len(res.Items) != 1 || res.Items[0].Reason != recommend.ReasonPopular ||
		res.RecommendationID != "rec_1" {
		t.Errorf("result = %+v", res)
	}
	got := models.recs[0]
	if got.User != "1000" || got.Item != nil || got.K != 5 || got.ExcludeSeen == nil || *got.ExcludeSeen {
		t.Errorf("request = %+v", got)
	}

	for name, args := range map[string]string{
		"neither":        `{"k": 3}`,
		"an object":      `[1]`,
		"unknown field":  `{"user": "1", "amount": 3}`,
		"fractional k":   `{"item": "a", "k": 2.5}`,
		"user and for":   `{"user": "1", "for": {"salesman": "R1"}}`,
		"rules, no for":  `{"user": "1", "rules": [{"kind": "pin", "ids": ["a"]}]}`,
		"window, no for": `{"user": "1", "within_days": 14}`,
		"unknown for":    `{"for": {"district": "N"}}`,
	} {
		if _, err := svc.RecommendTool(t.Context(), caller, tool, json.RawMessage(args)); err == nil {
			t.Errorf("%s: the call was not refused", name)
		}
	}
	if len(models.calls) != 1 {
		t.Errorf("a refused call reached the model: %v", models.calls)
	}

	res, err = svc.RecommendTool(t.Context(), caller, tool, json.RawMessage(
		`{"for": {"salesman": "R1"}, "period": {"from": "2026-10-05"}, "k": 8, "rules": [{"kind": "capacity", "per_day": 4}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.RecommendationID != "rec_2" || len(res.Results) != 1 || res.Results[0].Items[0].Reasons[0].Code != "overdue" || res.Items != nil {
		t.Errorf("scenario result = %+v", res)
	}
	sc := models.recs[1]
	if sc.For == nil || sc.For.Salesman != "R1" || sc.Period == nil || sc.Period.From != "2026-10-05" || sc.K != 8 ||
		len(sc.Rules) != 1 || sc.Rules[0].PerDay != 4 {
		t.Errorf("scenario request = %+v", sc)
	}
	// The customers due to order within 14 days, of everyone.
	if _, err := svc.RecommendTool(t.Context(), caller, tool, json.RawMessage(`{"for": {"all_customers": true}, "within_days": 14}`)); err != nil {
		t.Fatal(err)
	}
	if w := models.recs[2]; w.For == nil || !w.For.AllCustomers || w.WithinDays != 14 {
		t.Errorf("window request = %+v", w)
	}
	// Of one category and size only.
	if _, err := svc.RecommendTool(t.Context(), caller, tool, json.RawMessage(
		`{"for": {"all_customers": true, "segment": "Retail", "size": "large"}, "within_days": 14}`)); err != nil {
		t.Fatal(err)
	}
	if w := models.recs[3]; w.For == nil || w.For.Segment != "Retail" || w.For.Size != "large" {
		t.Errorf("category request = %+v", w)
	}
	// Of one brand and subcategory, with today's stock.
	if _, err := svc.RecommendTool(t.Context(), caller, tool, json.RawMessage(
		`{"for": {"customer": "C1", "brand": "Acme", "subcategory": "Soap"}, "stock": {"S1": 0, "S2": 12}}`)); err != nil {
		t.Fatal(err)
	}
	if w := models.recs[4]; w.For == nil || w.For.Brand != "Acme" || w.For.Subcategory != "Soap" || w.Stock["S1"] != 0 || w.Stock["S2"] != 12 {
		t.Errorf("brand request = %+v", w)
	}
	if _, ok := props["stock"]; !ok {
		t.Errorf("no stock: %v", props)
	}
	// Of one price tier, channel and region.
	if _, err := svc.RecommendTool(t.Context(), caller, tool, json.RawMessage(
		`{"for": {"product": "S1", "price_tier": "Gold", "channel": "Retail", "region": "N"}}`)); err != nil {
		t.Fatal(err)
	}
	if w := models.recs[5]; w.For == nil || w.For.PriceTier != "Gold" || w.For.Channel != "Retail" || w.For.Region != "N" {
		t.Errorf("tier request = %+v", w)
	}
	forProps, _ := props["for"].(map[string]any)["properties"].(map[string]any)
	for _, p := range []string{"all_customers", "segment", "size", "brand", "subcategory", "price_tier", "channel", "region"} {
		if _, ok := forProps[p]; !ok {
			t.Errorf("for has no %s: %v", p, forProps)
		}
	}
	models.calls = models.calls[:1]
	if _, err := svc.RecommendTool(t.Context(), editorOf("tenant-b"), tool, json.RawMessage(`{"user":"1"}`)); !errors.Is(err, mcpserver.ErrModelNotFound) {
		t.Errorf("a caller without the vhost: err = %v", err)
	}
	if _, err := svc.RecommendTool(t.Context(), caller, byName["predict_fraud"], json.RawMessage(`{"user":"1"}`)); err == nil {
		t.Error("a predict tool was called as a recommender")
	}
	if _, err := svc.PredictTool(t.Context(), caller, tool, json.RawMessage(`{"user":"1"}`)); err == nil {
		t.Error("a recommend tool was called as a predictor")
	}
}
