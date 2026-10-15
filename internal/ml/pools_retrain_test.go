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
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// A retrain policy keeps the whole training spec, device and custom script
// included, and trains through the same routing as a training by hand. Its
// device is checked when the policy is saved, not first when it fires.
func TestValidateRetrainPolicyChecksTheDevice(t *testing.T) {
	spec := func(algorithm, device string) worker.TrainSpec {
		return worker.TrainSpec{Dataset: "customers", Target: "churned", Algorithm: algorithm, Device: device}
	}
	tests := []struct {
		name    string
		spec    worker.TrainSpec
		wantErr string
	}{
		{"cpu", spec("", DeviceCPU), ""},
		{"gpu", spec("xgboost", DeviceGPU), ""},
		{"a custom script", spec(CustomPrefix+"tabnet", ""), ""},
		{"an unknown device", spec("", "tpu"), "device"},
		{"a custom script on the gpu", spec(CustomPrefix+"tabnet", DeviceGPU), "custom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRetrainPolicy(storage.MLRetrainPolicy{Schedule: "@daily", Spec: tt.spec})
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want one about %q", err, tt.wantErr)
			}
		})
	}
}
