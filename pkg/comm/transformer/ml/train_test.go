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

// fakeTrainer answers MLTrain and records what it was asked.
type fakeTrainer struct {
	vhost, model string
	req          map[string]any
	reply        map[string]any
	err          error
	calls        int
}

func (f *fakeTrainer) MLTrain(_ context.Context, vhost, model string, req map[string]any) (map[string]any, error) {
	f.calls++
	f.vhost, f.model, f.req = vhost, model, req
	return f.reply, f.err
}

func runTrain(t *testing.T, reg any, vhost string, cfg map[string]any) (hermod.Message, error) {
	t.Helper()
	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetData("tick", 1.0)
	if vhost != "" {
		msg.SetVHost(vhost)
	}
	ctx := context.WithValue(t.Context(), hermod.RegistryKey, reg)
	return (&Train{}).Transform(ctx, msg, cfg)
}

func TestTrainSendsTheSpecAndWritesTheResult(t *testing.T) {
	reg := &fakeTrainer{reply: map[string]any{"model": "churn", "version": "4", "live": true, "metrics": map[string]any{"score": 0.88}}}
	out, err := runTrain(t, reg, "tenant-a", map[string]any{
		"model": "churn", "dataset": "customers", "target": "churned",
		"features": `["age","plan"]`, "task": "classification", "algorithm": "xgboost",
		"goLive": "if", "goLiveMetric": "f1", "goLiveMin": "0.8", "goLiveMax": 0.99,
		"sourceId": "src-1", "query": "SELECT * FROM customers", "maxRows": "5000",
	})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if reg.vhost != "tenant-a" || reg.model != "churn" {
		t.Errorf("trained %s/%s", reg.vhost, reg.model)
	}
	want := map[string]any{
		"dataset": "customers", "target": "churned", "features": []string{"age", "plan"},
		"task": "classification", "algorithm": "xgboost",
		"goLive":   map[string]any{"mode": "if", "metric": "f1", "min": 0.8, "max": 0.99},
		"sourceId": "src-1", "query": "SELECT * FROM customers", "maxRows": 5000,
	}
	if !reflect.DeepEqual(reg.req, want) {
		t.Errorf("request =\n%v\nwant\n%v", reg.req, want)
	}
	got, _ := out.Data()["training"].(map[string]any)
	if got["version"] != "4" || got["live"] != true {
		t.Errorf("training = %v, want the result under the default field", out.Data()["training"])
	}
	if out.Data()["tick"] != 1.0 {
		t.Error("the message lost its own fields")
	}
}

func TestTrainDefaultsAndCommaFeatures(t *testing.T) {
	reg := &fakeTrainer{reply: map[string]any{"version": "1"}}
	out, err := runTrain(t, reg, "", map[string]any{
		"model": "m", "dataset": "d", "target": "y", "features": "a, b ,", "outputField": "run",
	})
	if err != nil {
		t.Fatal(err)
	}
	if reg.vhost != "default" {
		t.Errorf("vhost = %q", reg.vhost)
	}
	if !reflect.DeepEqual(reg.req["features"], []string{"a", "b"}) {
		t.Errorf("features = %v", reg.req["features"])
	}
	if reg.req["task"] != "auto" || reg.req["algorithm"] != "auto" {
		t.Errorf("task/algorithm = %v/%v, want auto", reg.req["task"], reg.req["algorithm"])
	}
	if gl, _ := reg.req["goLive"].(map[string]any); gl["mode"] != "never" {
		t.Errorf("goLive = %v, want never when the node says nothing", reg.req["goLive"])
	}
	if _, ok := reg.req["sourceId"]; ok {
		t.Error("a refresh was asked for with no source")
	}
	if out.Data()["run"] == nil {
		t.Error("the result is not under the named output field")
	}
}

func TestTrainRefusesWhatItCannotDo(t *testing.T) {
	ok := map[string]any{"model": "m", "dataset": "d", "target": "y"}
	with := func(k string, v any) map[string]any {
		c := map[string]any{}
		for kk, vv := range ok {
			c[kk] = vv
		}
		c[k] = v
		return c
	}
	cases := []struct {
		name string
		reg  any
		cfg  map[string]any
		want string
	}{
		{"no model", &fakeTrainer{}, with("model", ""), "model"},
		{"no dataset", &fakeTrainer{}, with("dataset", ""), "dataset"},
		{"no target", &fakeTrainer{}, with("target", ""), "target"},
		{"bad min", &fakeTrainer{}, with("goLiveMin", "lots"), "goLiveMin"},
		{"query without source", &fakeTrainer{}, with("query", "SELECT 1"), "source"},
		{"no registry", nil, ok, "registry"},
		{"training error", &fakeTrainer{err: errors.New("worker down")}, ok, "worker down"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := runTrain(t, tc.reg, "v", tc.cfg)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}
