// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package ml

import (
	"context"
	"errors"
	"fmt"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/transformer"
	"github.com/gsoultan/hermod/pkg/comm/transformer/core"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
)

func init() {
	transformer.Register("ml_feedback", &Feedback{})
}

// Where the Feedback node reads and writes when its config names nothing:
// the prediction id as the Predict node writes it, and "feedback" for the
// verdict.
const (
	defaultPredictionIDField = "prediction.prediction_id"
	defaultFeedbackField     = "feedback"
)

// feedbackCaller is what the engine's registry offers: the real answer to one
// of a vhost's model's earlier predictions, with whether the model was right.
type feedbackCaller interface {
	MLFeedback(ctx context.Context, vhost, model, predictionID string, actual any) (bool, error)
}

// Feedback tells a model what the right answer to one of its predictions was,
// once the message that knows it comes through: a customer who did leave, an
// invoice that was paid late. The model's server then tracks how often the
// model is right in use. Only a model on a mimir server takes feedback.
//
// Config:
//   - model: the model's name in the vhost's model registry. Required.
//   - predictionIdField: where the prediction id is on the message, default
//     "prediction.prediction_id" (where the Predict node leaves it).
//   - actualField: the field holding the real answer: the label, or the number
//     for a model that answers with one. Required.
//   - outputField: where the verdict goes, default "feedback":
//     {"right": bool, "prediction_id": id}.
//
// The engine's onError ("fail", "continue", "drop") and statusField apply as
// for every transformer.
type Feedback struct{}

func (f *Feedback) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}
	model := core.GetConfigString(config, "model")
	if model == "" {
		return msg, errors.New("ml_feedback: choose a model")
	}
	actualField := core.GetConfigString(config, "actualField")
	if actualField == "" {
		return msg, errors.New("ml_feedback: name the field that holds the real answer")
	}
	idField := core.GetConfigString(config, "predictionIdField")
	if idField == "" {
		idField = defaultPredictionIDField
	}
	caller, ok := ctx.Value(hermod.RegistryKey).(feedbackCaller)
	if !ok {
		return msg, errors.New("ml_feedback: no model registry is available")
	}

	id, _ := evaluator.GetMsgValByPath(msg, idField).(string)
	if id == "" {
		return msg, fmt.Errorf("ml_feedback: the record has no prediction id at %q", idField)
	}
	actual := evaluator.GetMsgValByPath(msg, actualField)
	if actual == nil {
		return msg, fmt.Errorf("ml_feedback: the record has no real answer at %q", actualField)
	}

	vhost := defaultVHost
	if scoped, ok := msg.(hermod.VHostScoped); ok && scoped.VHost() != "" {
		vhost = scoped.VHost()
	}
	right, err := caller.MLFeedback(ctx, vhost, model, id, actual)
	if err != nil {
		return msg, fmt.Errorf("ml_feedback: %w", err)
	}

	field := core.GetConfigString(config, "outputField")
	if field == "" {
		field = defaultFeedbackField
	}
	msg.SetData(field, map[string]any{"right": right, "prediction_id": id})
	return msg, nil
}
