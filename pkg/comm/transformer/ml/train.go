package ml

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/transformer"
	"github.com/gsoultan/hermod/pkg/comm/transformer/core"
)

func init() {
	transformer.Register("ml_train", &Train{})
}

// defaultTrainField is where the training's result goes when the node names
// nothing.
const defaultTrainField = "training"

// modelTrainer is what the engine's registry offers: a training of one of the
// vhost's models on Hermod's ML worker.
type modelTrainer interface {
	MLTrain(ctx context.Context, vhost, model string, req map[string]any) (map[string]any, error)
}

// Train trains a new version of a vhost's model on a dataset, each time a
// message reaches it, and writes the result onto the message: the version,
// its metrics, and whether it went live. It belongs in a workflow that runs on
// a schedule or on demand, not one that sees every change to a table.
//
// Config:
//   - model, dataset, target: required.
//   - features: the columns to learn from, as a JSON list or comma-separated;
//     empty means every column but the target.
//   - task: auto, classification or regression. algorithm: auto,
//     random_forest, gradient_boosting, linear, xgboost, or custom:NAME for
//     one of the vhost's custom training scripts.
//   - device: cpu (the default) or gpu, which trains on the GPU worker pool.
//   - goLive: never (the default), always, or if; goLiveMetric (default
//     "score"), goLiveMin and goLiveMax bound "if".
//   - sourceId and query: refill the dataset from that database first, with
//     at most maxRows rows.
//   - outputField: where the result goes, default "training".
type Train struct{}

func (t *Train) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}
	model := core.GetConfigString(config, "model")
	req, err := trainRequest(config)
	if model == "" {
		return msg, errors.New("ml_train: name the model to train")
	}
	if err != nil {
		return msg, err
	}
	trainer, ok := ctx.Value(hermod.RegistryKey).(modelTrainer)
	if !ok {
		return msg, errors.New("ml_train: no model registry is available")
	}

	vhost := defaultVHost
	if scoped, ok := msg.(hermod.VHostScoped); ok && scoped.VHost() != "" {
		vhost = scoped.VHost()
	}
	res, err := trainer.MLTrain(ctx, vhost, model, req)
	if err != nil {
		return msg, fmt.Errorf("ml_train: %w", err)
	}

	field := core.GetConfigString(config, "outputField")
	if field == "" {
		field = defaultTrainField
	}
	msg.SetData(field, res)
	return msg, nil
}

// trainRequest turns the node's settings into the registry's request.
func trainRequest(config map[string]any) (map[string]any, error) {
	req := map[string]any{
		"dataset":   core.GetConfigString(config, "dataset"),
		"target":    core.GetConfigString(config, "target"),
		"task":      orDefault(core.GetConfigString(config, "task"), "auto"),
		"algorithm": orDefault(core.GetConfigString(config, "algorithm"), "auto"),
	}
	if req["dataset"] == "" {
		return nil, errors.New("ml_train: name the dataset to train on")
	}
	if req["target"] == "" {
		return nil, errors.New("ml_train: name the target column the model predicts")
	}
	if device := core.GetConfigString(config, "device"); device != "" {
		req["device"] = device
	}
	features, err := parseFeatures(config["features"])
	if err != nil {
		return nil, err
	}
	if len(features) > 0 {
		req["features"] = features
	}

	goLive := map[string]any{"mode": orDefault(core.GetConfigString(config, "goLive"), "never")}
	if m := core.GetConfigString(config, "goLiveMetric"); m != "" {
		goLive["metric"] = m
	}
	for _, k := range []string{"goLiveMin", "goLiveMax"} {
		n, ok, err := number(config[k])
		if err != nil {
			return nil, fmt.Errorf("ml_train: %s %w", k, err)
		}
		if ok {
			goLive[strings.ToLower(strings.TrimPrefix(k, "goLive"))] = n
		}
	}
	req["goLive"] = goLive

	source, query := core.GetConfigString(config, "sourceId"), core.GetConfigString(config, "query")
	switch {
	case query != "" && source == "":
		return nil, errors.New("ml_train: a dataset query needs the database source it runs on")
	case source != "" && query != "":
		req["sourceId"], req["query"] = source, query
		n, ok, err := number(config["maxRows"])
		if err != nil {
			return nil, fmt.Errorf("ml_train: maxRows %w", err)
		}
		if ok {
			req["maxRows"] = int(n)
		}
	}
	return req, nil
}

// parseFeatures reads a JSON list, a comma-separated list, or a list value.
func parseFeatures(v any) ([]string, error) {
	var out []string
	switch in := v.(type) {
	case nil:
		return nil, nil
	case []string:
		out = in
	case []any:
		for _, x := range in {
			s, ok := x.(string)
			if !ok {
				return nil, fmt.Errorf("ml_train: features must be column names, not %T", x)
			}
			out = append(out, s)
		}
	case string:
		s := strings.TrimSpace(in)
		if strings.HasPrefix(s, "[") {
			if err := json.Unmarshal([]byte(s), &out); err != nil {
				return nil, fmt.Errorf("ml_train: features must be a JSON list of column names: %w", err)
			}
		} else {
			out = strings.Split(s, ",")
		}
	default:
		return nil, fmt.Errorf("ml_train: features must be a list of column names, not %T", v)
	}
	clean := out[:0:0]
	for _, f := range out {
		if f = strings.TrimSpace(f); f != "" {
			clean = append(clean, f)
		}
	}
	return clean, nil
}

// number reads a setting the editor saves as text and an API client may send
// as a number; an empty one is absent.
func number(v any) (float64, bool, error) {
	switch n := v.(type) {
	case nil:
		return 0, false, nil
	case float64:
		return n, true, nil
	case int:
		return float64(n), true, nil
	case string:
		if strings.TrimSpace(n) == "" {
			return 0, false, nil
		}
		f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		if err != nil {
			return 0, false, fmt.Errorf("must be a number, not %q", n)
		}
		return f, true, nil
	}
	return 0, false, fmt.Errorf("must be a number, not %T", v)
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
