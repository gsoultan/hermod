// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package nodeclean_test

import (
	"context"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/internal/storage/nodeclean"
)

type fakeSecrets struct{ m map[string]string }

func (f *fakeSecrets) GetVHostSecret(_ context.Context, vhost, name string) (storage.VHostSecret, error) {
	v, ok := f.m[vhost+"/"+name]
	if !ok {
		return storage.VHostSecret{}, storage.ErrNotFound
	}
	return storage.VHostSecret{VHost: vhost, Name: name, Value: v}, nil
}

func (f *fakeSecrets) PutVHostSecret(_ context.Context, s storage.VHostSecret) error {
	f.m[s.VHost+"/"+s.Name] = s.Value
	return nil
}

func TestMoveCredentialsLeavesOnlyReferences(t *testing.T) {
	store := &fakeSecrets{m: map[string]string{}}
	nodes := []storage.WorkflowNode{
		step("api_lookup", map[string]any{"label": "lookup", "username": "apiuser", "password": "pw1"}),
		{ID: "node_2", Type: "transformation", Config: map[string]any{"transType": "api_lookup", "label": "lookup", "password": "pw2"}},
	}
	out, moved, err := nodeclean.MoveCredentials(t.Context(), store, "", "abcdef12-0000", nodes)
	if err != nil {
		t.Fatal(err)
	}
	if len(moved) != 3 {
		t.Fatalf("moved = %v", moved)
	}
	if got := out[0].Config["password"]; got != "{{secret.WF_ABCDEF12_NODE_1_PASSWORD}}" {
		t.Fatalf("password = %v", got)
	}
	if store.m["default/WF_ABCDEF12_NODE_1_PASSWORD"] != "pw1" {
		t.Fatalf("store = %v", store.m)
	}
	// Two steps with one label must not share a secret.
	ref2, _ := out[1].Config["password"].(string)
	if ref2 == out[0].Config["password"] || !strings.HasPrefix(ref2, "{{secret.") {
		t.Fatalf("second step's password = %v", ref2)
	}
	name2 := strings.TrimSuffix(strings.TrimPrefix(ref2, "{{secret."), "}}")
	if store.m["default/"+name2] != "pw2" {
		t.Fatalf("second value stored as %q: %v", name2, store.m)
	}
	if len(nodeclean.TypedCredentials(out)) != 0 {
		t.Fatal("credentials remain")
	}
	// Saving the result again moves nothing.
	if _, again, _ := nodeclean.MoveCredentials(t.Context(), store, "", "abcdef12-0000", out); len(again) != 0 {
		t.Fatalf("moved again: %v", again)
	}
}

func TestMoveCredentialsRefusesAValueASecretCannotHold(t *testing.T) {
	store := &fakeSecrets{m: map[string]string{}}
	nodes := []storage.WorkflowNode{step("ai_prompt", map[string]any{"apiKey": strings.Repeat("k", storage.MaxVHostSecretValueLen+1)})}
	if _, _, err := nodeclean.MoveCredentials(t.Context(), store, "default", "wf", nodes); err == nil {
		t.Fatal("want an error for a value too long to store")
	}
}

func TestMovedSecretsNamesWhatASaveWrote(t *testing.T) {
	store := &fakeSecrets{m: map[string]string{}}
	sent := []storage.WorkflowNode{step("api_lookup", map[string]any{"label": "l", "password": "p",
		"headers": `{"Authorization":"Bearer x"}`})}
	saved, _, err := nodeclean.MoveCredentials(t.Context(), store, "default", "wf", sent)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(nodeclean.MovedSecrets(sent, saved), ",")
	if got != "WF_WF_L_PASSWORD,WF_WF_L_AUTHORIZATION" && got != "WF_WF_L_AUTHORIZATION,WF_WF_L_PASSWORD" {
		t.Fatalf("moved = %s", got)
	}
}
