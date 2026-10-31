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
	"reflect"
	"strings"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// fakeFeedback answers MLFeedback and records what it was told.
type fakeFeedback struct {
	vhost, model, id string
	actual           any
	right            bool
	err              error
}

func (f *fakeFeedback) MLFeedback(_ context.Context, vhost, model, id string, actual any) (bool, error) {
	f.vhost, f.model, f.id, f.actual = vhost, model, id, actual
	return f.right, f.err
}

func runFeedback(t *testing.T, reg any, vhost string, cfg, fields map[string]any) (hermod.Message, error) {
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
	return (&Feedback{}).Transform(ctx, msg, cfg)
}

func TestFeedbackSendsThePredictionIdAndTheRealAnswerAndWritesTheVerdict(t *testing.T) {
	reg := &fakeFeedback{right: true}
	out, err := runFeedback(t, reg, "tenant-a",
		map[string]any{"model": "churn", "actualField": "outcome"},
		map[string]any{"prediction": map[string]any{"prediction": "left", "prediction_id": "p-7"}, "outcome": "left"})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if reg.vhost != "tenant-a" || reg.model != "churn" || reg.id != "p-7" || reg.actual != "left" {
		t.Errorf("told %s/%s id=%q actual=%v", reg.vhost, reg.model, reg.id, reg.actual)
	}
	if got := out.Data()["feedback"]; !reflect.DeepEqual(got, map[string]any{"right": true, "prediction_id": "p-7"}) {
		t.Errorf("feedback = %v", got)
	}
}

func TestFeedbackReadsTheIdAndWritesWhereTold(t *testing.T) {
	reg := &fakeFeedback{right: false}
	out, err := runFeedback(t, reg, "",
		map[string]any{"model": "price", "predictionIdField": "pid", "actualField": "paid.amount", "outputField": "verdict"},
		map[string]any{"pid": "p-1", "paid": map[string]any{"amount": 41.5}})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if reg.vhost != "default" || reg.id != "p-1" || reg.actual != 41.5 {
		t.Errorf("told %s id=%q actual=%v", reg.vhost, reg.id, reg.actual)
	}
	if got, _ := out.Data()["verdict"].(map[string]any); got["right"] != false {
		t.Errorf("verdict = %v", out.Data()["verdict"])
	}
}

func TestFeedbackRefusesWhatItCannotDo(t *testing.T) {
	cases := []struct {
		name   string
		reg    any
		cfg    map[string]any
		fields map[string]any
		want   string
	}{
		{"no model", &fakeFeedback{}, map[string]any{"actualField": "a"}, map[string]any{}, "model"},
		{"no actual field", &fakeFeedback{}, map[string]any{"model": "m"}, map[string]any{}, "real answer"},
		{"no registry", nil, map[string]any{"model": "m", "actualField": "a"}, map[string]any{}, "registry"},
		{"no id on the record", &fakeFeedback{}, map[string]any{"model": "m", "actualField": "a"}, map[string]any{"a": "x"}, "prediction id"},
		{"no answer on the record", &fakeFeedback{}, map[string]any{"model": "m", "actualField": "a"},
			map[string]any{"prediction": map[string]any{"prediction_id": "p"}}, `no real answer at "a"`},
		{"model error", &fakeFeedback{err: errors.New("only a model on a mimir server")}, map[string]any{"model": "m", "actualField": "a"},
			map[string]any{"prediction": map[string]any{"prediction_id": "p"}, "a": "x"}, "mimir"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := runFeedback(t, tc.reg, "v", tc.cfg, tc.fields)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}
