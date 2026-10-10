// Package ml holds the machine-learning transformers.
package ml

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/transformer"
	"github.com/gsoultan/hermod/pkg/comm/transformer/core"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
)

func init() {
	transformer.Register("ml_predict", &Predict{})
}

// defaultVHost is the vhost of a workflow that names none.
const defaultVHost = "default"

// defaultOutputField is where the prediction goes when the node names nothing.
const defaultOutputField = "prediction"

// modelCaller is what the engine's registry offers: a call to one of the
// vhost's models. The registry resolves the model's server and token.
type modelCaller interface {
	MLPredict(ctx context.Context, vhost, model string, rows []map[string]any) ([]map[string]any, error)
}

// Predict calls a model registered in the workflow's vhost with fields of the
// message and writes the prediction back onto it.
//
// Config:
//   - model: the model's name in the vhost's model registry. Required.
//   - inputs: feature name -> field path, as an object or JSON text. Empty
//     sends the whole record.
//   - outputField: where the prediction goes, default "prediction". A model
//     with one output writes that value; one with several writes an object.
//
// The engine's onError ("fail", "continue", "drop") and statusField apply as
// for every transformer.
type Predict struct{}

func (p *Predict) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}
	model := core.GetConfigString(config, "model")
	if model == "" {
		return msg, errors.New("ml_predict: choose a model")
	}
	inputs, err := parseInputs(config["inputs"])
	if err != nil {
		return msg, err
	}
	caller, ok := ctx.Value(hermod.RegistryKey).(modelCaller)
	if !ok {
		return msg, errors.New("ml_predict: no model registry is available")
	}

	row, err := buildRow(msg, inputs)
	if err != nil {
		return msg, err
	}

	vhost := defaultVHost
	if scoped, ok := msg.(hermod.VHostScoped); ok && scoped.VHost() != "" {
		vhost = scoped.VHost()
	}
	preds, err := caller.MLPredict(ctx, vhost, model, []map[string]any{row})
	if err != nil {
		return msg, fmt.Errorf("ml_predict: %w", err)
	}
	if len(preds) != 1 {
		return msg, fmt.Errorf("ml_predict: model %q returned %d predictions for one record", model, len(preds))
	}

	field := core.GetConfigString(config, "outputField")
	if field == "" {
		field = defaultOutputField
	}
	msg.SetData(field, outputValue(preds[0]))
	return msg, nil
}

// parseInputs reads the feature mapping, which the editor saves as JSON text
// and an API client may send as an object.
func parseInputs(v any) (map[string]string, error) {
	switch in := v.(type) {
	case nil:
		return nil, nil
	case string:
		if in == "" {
			return nil, nil
		}
		var m map[string]string
		if err := json.Unmarshal([]byte(in), &m); err != nil {
			return nil, fmt.Errorf("ml_predict: inputs must be a JSON object of feature to field: %w", err)
		}
		return m, nil
	case map[string]any:
		m := make(map[string]string, len(in))
		for k, path := range in {
			s, ok := path.(string)
			if !ok {
				return nil, fmt.Errorf("ml_predict: inputs: feature %q must map to a field path", k)
			}
			m[k] = s
		}
		return m, nil
	case map[string]string:
		return in, nil
	}
	return nil, fmt.Errorf("ml_predict: inputs must be a JSON object of feature to field, not %T", v)
}

// buildRow is what the model is sent: the mapped fields, or the whole record.
// A mapped field the message does not have is an error, not a null sent to the
// model, which would answer something meaningless.
func buildRow(msg hermod.Message, inputs map[string]string) (map[string]any, error) {
	if len(inputs) == 0 {
		return msg.Data(), nil
	}
	row := make(map[string]any, len(inputs))
	for feature, path := range inputs {
		v := evaluator.GetMsgValByPath(msg, path)
		if v == nil {
			return nil, fmt.Errorf("ml_predict: the record has no field %q for feature %q", path, feature)
		}
		row[feature] = v
	}
	return row, nil
}

// outputValue unwraps a model's only output, and keeps several as an object.
func outputValue(pred map[string]any) any {
	if len(pred) == 1 {
		for _, v := range pred {
			return v
		}
	}
	return pred
}
