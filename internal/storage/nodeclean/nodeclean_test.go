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
	"encoding/json"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/internal/storage/nodeclean"
)

// editorSourceNode is a source node exactly as the editor saved it before
// this package existed: its own settings, plus the whole source the drawer
// returned spread over them (handleInlineSave). Taken from a live save on a
// dev instance, password and all.
func editorSourceNode() storage.WorkflowNode {
	var cfg map[string]any
	_ = json.Unmarshal([]byte(`{"active": true, "config": {"dbname": "app", "host": "db.internal",
		"password": "S3cretPW-src", "port": "5432", "tables": "public.users", "user": "svc_reader"},
		"created_at": "0001-01-01T00:00:00Z", "id": "src-1", "label": "pg-src", "name": "pg-src",
		"ref_id": "src-1", "state": null, "type": "postgres", "vhost": "default", "worker_id": "",
		"sample": "{\"after\":{\"email\":\"a@b.c\"}}", "status": "running", "workspace_id": "ws"}`), &cfg)
	return storage.WorkflowNode{ID: "n1", Type: "source", RefID: "src-1", Config: cfg}
}

func TestASourceNodeKeepsOnlyItsOwnSettings(t *testing.T) {
	out, changed := nodeclean.Nodes([]storage.WorkflowNode{editorSourceNode()})
	if !changed {
		t.Fatal("a node carrying a copy of its source must be reported as changed")
	}
	want := map[string]any{"label": "pg-src", "type": "postgres"}
	got := out[0].Config
	if len(got) != len(want) || got["label"] != want["label"] || got["type"] != want["type"] {
		t.Fatalf("config = %v, want %v", got, want)
	}
	if out[0].RefID != "src-1" {
		t.Fatalf("ref_id lost: %q", out[0].RefID)
	}
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "S3cretPW") || strings.Contains(string(raw), "svc_reader") {
		t.Fatalf("credentials survived: %s", raw)
	}
}

func TestASinkNodeKeepsItsExecutionSettings(t *testing.T) {
	n := storage.WorkflowNode{ID: "n3", Type: "sink", RefID: "snk", Config: map[string]any{
		"label": "wh", "type": "postgres", "sequential": true, "transaction": "orders",
		"max_prepared_age": "5m", "config": map[string]any{"password": "pw"}, "name": "wh",
		"active": true, "vhost": "default", "worker_id": "w1",
	}}
	out, _ := nodeclean.Nodes([]storage.WorkflowNode{n})
	for _, k := range []string{"label", "type", "sequential", "transaction", "max_prepared_age"} {
		if _, ok := out[0].Config[k]; !ok {
			t.Errorf("sink node lost %q", k)
		}
	}
	for _, k := range []string{"config", "name", "active", "vhost", "worker_id"} {
		if _, ok := out[0].Config[k]; ok {
			t.Errorf("sink node kept %q", k)
		}
	}
}

func TestANameWithoutALabelBecomesTheLabel(t *testing.T) {
	n := storage.WorkflowNode{ID: "n1", Type: "source", Config: map[string]any{"name": "orders db"}}
	out, _ := nodeclean.Nodes([]storage.WorkflowNode{n})
	if out[0].Config["label"] != "orders db" {
		t.Fatalf("label = %v", out[0].Config["label"])
	}
}

func TestCapturedDataAndTheDuplicateRefIDLeaveEveryNode(t *testing.T) {
	n := storage.WorkflowNode{ID: "n2", Type: "transformation", RefID: "", Config: map[string]any{
		"transType": "mapping", "label": "map", "ref_id": "stale-after-a-copy",
		"lastSample": map[string]any{"email": "a@b.c"}, "testResult": map[string]any{"payload": 1},
		"mapping": "{}",
	}}
	out, changed := nodeclean.Nodes([]storage.WorkflowNode{n})
	if !changed {
		t.Fatal("want changed")
	}
	for _, k := range []string{"ref_id", "lastSample", "testResult"} {
		if _, ok := out[0].Config[k]; ok {
			t.Errorf("kept %q", k)
		}
	}
	if out[0].Config["mapping"] != "{}" || out[0].Config["transType"] != "mapping" {
		t.Fatalf("a setting went missing: %v", out[0].Config)
	}
}

// A subflow call is named in config; only connections lose "name".
func TestOtherNodesKeepTheirName(t *testing.T) {
	n := storage.WorkflowNode{ID: "n4", Type: "subflow_call", Config: map[string]any{"name": "enrich", "label": "x"}}
	out, changed := nodeclean.Nodes([]storage.WorkflowNode{n})
	if changed || out[0].Config["name"] != "enrich" {
		t.Fatalf("changed=%v config=%v", changed, out[0].Config)
	}
}

func TestCleanNodesAreReturnedAsTheyCameAndInputIsNotModified(t *testing.T) {
	in := []storage.WorkflowNode{editorSourceNode()}
	before, _ := json.Marshal(in)
	out, _ := nodeclean.Nodes(in)
	after, _ := json.Marshal(in)
	if string(before) != string(after) {
		t.Fatal("Nodes modified the nodes it was given")
	}
	again, changed := nodeclean.Nodes(out)
	if changed {
		t.Fatalf("cleaning is not idempotent: %v", again[0].Config)
	}
}
