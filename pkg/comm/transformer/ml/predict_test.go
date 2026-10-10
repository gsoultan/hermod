package ml

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// fakeModels answers MLPredict and records what it was asked.
type fakeModels struct {
	vhost, model string
	rows         []map[string]any
	reply        []map[string]any
	err          error
}

func (f *fakeModels) MLPredict(_ context.Context, vhost, model string, rows []map[string]any) ([]map[string]any, error) {
	f.vhost, f.model, f.rows = vhost, model, rows
	return f.reply, f.err
}

func run(t *testing.T, reg any, vhost string, cfg, fields map[string]any) (hermod.Message, error) {
	t.Helper()
	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	for k, v := range fields {
		msg.SetData(k, v)
	}
	if vhost != "" {
		msg.SetVHost(vhost)
	}
	ctx := context.WithValue(t.Context(), hermod.RegistryKey, reg)
	return (&Predict{}).Transform(ctx, msg, cfg)
}

func TestPredictMapsFieldsToFeaturesAndWritesTheSingleOutput(t *testing.T) {
	reg := &fakeModels{reply: []map[string]any{{"prediction": 0.93}}}
	out, err := run(t, reg, "tenant-a", map[string]any{
		"model":       "fraud",
		"inputs":      `{"amount": "order.total", "country": "country"}`,
		"outputField": "fraud_score",
	}, map[string]any{"order": map[string]any{"total": 120.5}, "country": "ID", "noise": "x"})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if reg.vhost != "tenant-a" || reg.model != "fraud" {
		t.Errorf("called %s/%s, want tenant-a/fraud", reg.vhost, reg.model)
	}
	if want := []map[string]any{{"amount": 120.5, "country": "ID"}}; !reflect.DeepEqual(reg.rows, want) {
		t.Errorf("rows = %v, want %v", reg.rows, want)
	}
	if got := out.Data()["fraud_score"]; got != 0.93 {
		t.Errorf("fraud_score = %v, want the single output", got)
	}
}

func TestPredictWithoutInputsSendsTheWholeRecordAndKeepsSeveralOutputs(t *testing.T) {
	reg := &fakeModels{reply: []map[string]any{{"label": 1.0, "probabilities": []any{0.2, 0.8}}}}
	out, err := run(t, reg, "", map[string]any{"model": "churn"}, map[string]any{"age": 30.0})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if reg.vhost != "default" {
		t.Errorf("vhost = %q, want default for a workflow with none", reg.vhost)
	}
	if !reflect.DeepEqual(reg.rows, []map[string]any{{"age": 30.0}}) {
		t.Errorf("rows = %v", reg.rows)
	}
	got, _ := out.Data()["prediction"].(map[string]any)
	if got["label"] != 1.0 {
		t.Errorf("prediction = %v, want both outputs under the default field", out.Data()["prediction"])
	}
}

func TestPredictInputsMayBeAMapAsWellAsJSONText(t *testing.T) {
	reg := &fakeModels{reply: []map[string]any{{"prediction": 1.0}}}
	_, err := run(t, reg, "v", map[string]any{"model": "m", "inputs": map[string]any{"a": "x"}}, map[string]any{"x": 5.0})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reg.rows, []map[string]any{{"a": 5.0}}) {
		t.Errorf("rows = %v", reg.rows)
	}
}

func TestPredictRefusesWhatItCannotDo(t *testing.T) {
	cases := []struct {
		name string
		reg  any
		cfg  map[string]any
		want string
	}{
		{"no model", &fakeModels{}, map[string]any{}, "model"},
		{"bad inputs", &fakeModels{}, map[string]any{"model": "m", "inputs": "{"}, "inputs"},
		{"no registry", nil, map[string]any{"model": "m"}, "registry"},
		{"model error", &fakeModels{err: errors.New("server down")}, map[string]any{"model": "m"}, "server down"},
		{"no prediction", &fakeModels{reply: nil}, map[string]any{"model": "m"}, "prediction"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := run(t, tc.reg, "v", tc.cfg, map[string]any{"x": 1.0})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

func TestPredictRefusesAMissingInputField(t *testing.T) {
	reg := &fakeModels{reply: []map[string]any{{"prediction": 1.0}}}
	_, err := run(t, reg, "v", map[string]any{"model": "m", "inputs": `{"amount":"total"}`}, map[string]any{"x": 1.0})
	if err == nil || !strings.Contains(err.Error(), "total") {
		t.Fatalf("err = %v, want a refusal naming the missing field", err)
	}
	if reg.model != "" {
		t.Error("the model was called with a feature missing")
	}
}
