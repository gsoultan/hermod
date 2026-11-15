// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package registry

import (
	"context"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
)

func missingSecretWorkflow(value any) storage.Workflow {
	wf := kafkaEOSWorkflow("cdc", "pg")
	wf.VHost = "sales"
	wf.Nodes = append(wf.Nodes, storage.WorkflowNode{
		ID: "T", Type: "transformation",
		Config: map[string]any{"transType": "set", "column.token": value},
	})
	return wf
}

// A secret that does not exist answered an empty string, with nothing logged:
// secret("API_KEY") in a header sent "Authorization: Bearer " to the other
// side, for every message, and the workflow reported itself healthy. A
// connector's setting that names a missing secret already fails the connector
// (resolveSecrets); a step's now fails the start the same way, naming the step
// and the secret.
func TestAStepReadingAMissingSecretStopsTheWorkflowFromStarting(t *testing.T) {
	r := kafkaEOSRegistry()
	r.SetSecretManager(keyedSecretManager{"PRESENT": "value"})

	for _, value := range []any{
		`secret("API_KEY")`,
		`concat("Bearer ", secret.API_KEY)`,
		`{{secret.API_KEY}}`,
		map[string]any{"headers": []any{map[string]any{"Authorization": `{{secret("API_KEY")}}`}}},
	} {
		err := r.ValidateWorkflow(context.Background(), missingSecretWorkflow(value))
		if err == nil {
			t.Errorf("a step reading the missing secret API_KEY as %v was allowed to start", value)
			continue
		}
		for _, want := range []string{"T", "API_KEY", "sales"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the error for %v should name %q, got: %v", value, want, err)
			}
		}
	}
}

func TestAStepReadingASecretThatExistsOrHasAFallbackStarts(t *testing.T) {
	r := kafkaEOSRegistry()
	r.SetSecretManager(keyedSecretManager{"PRESENT": "value"})

	for _, value := range []any{
		`secret("PRESENT")`,
		`secret("API_KEY", "anonymous")`, // answers its fallback
		`source.secret.token`,            // a message field
		`plain text about a secret`,
	} {
		if err := r.ValidateWorkflow(context.Background(), missingSecretWorkflow(value)); err != nil {
			t.Errorf("a step whose value is %v was refused: %v", value, err)
		}
	}
}
