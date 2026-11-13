// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package handlers

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
)

// auditSpy keeps what RecordAuditLog writes to the two tables.
type auditSpy struct {
	storage.Storage
	logs   []storage.Log
	audits []storage.AuditLog
}

func (s *auditSpy) CreateLog(_ context.Context, l storage.Log) error {
	s.logs = append(s.logs, l)
	return nil
}

func (s *auditSpy) CreateAuditLog(_ context.Context, a storage.AuditLog) error {
	s.audits = append(s.audits, a)
	return nil
}

func recordAudit(t *testing.T, data any) (logData, auditPayload string) {
	t.Helper()
	spy := &auditSpy{}
	h := &Handler{LogStorage: spy}
	h.RecordAuditLog(httptest.NewRequestWithContext(t.Context(), "POST", "/api/sources", nil), "INFO", "Created source", "CREATE", "", "src-1", "", data)
	if len(spy.logs) != 1 || len(spy.audits) != 1 {
		t.Fatalf("want one row in each table, got %d logs and %d audit rows", len(spy.logs), len(spy.audits))
	}
	return spy.logs[0].Data, spy.audits[0].Payload
}

// A source or sink is audited with its whole configuration. The credentials
// in it are encrypted in the sources table, so the audit trail must not be
// the place they can be read in the clear.
func TestAuditTrailDoesNotKeepConnectorCredentials(t *testing.T) {
	src := storage.Source{
		ID: "src-1", Name: "orders", Type: "postgres", VHost: "tenanta",
		Config: map[string]string{
			"host":              "db.internal",
			"password":          "PW-LEAK",
			"api_key":           "APIKEY-LEAK",
			"connection_string": "postgres://u:DSN-LEAK@db/x",
			"partition_key":     "order_id",
		},
	}
	for name, got := range both(recordAudit(t, src)) {
		for _, leak := range []string{"PW-LEAK", "APIKEY-LEAK", "DSN-LEAK"} {
			if strings.Contains(got, leak) {
				t.Errorf("%s still holds %s: %s", name, leak, got)
			}
		}
		for _, kept := range []string{"db.internal", "order_id", "orders", "tenanta"} {
			if !strings.Contains(got, kept) {
				t.Errorf("%s lost the non-secret value %q: %s", name, kept, got)
			}
		}
	}
}

// A workflow's nodes spell their settings in camelCase and nest them.
func TestAuditTrailDoesNotKeepNodeCredentials(t *testing.T) {
	wf := storage.Workflow{
		ID: "wf-1", Name: "enrich",
		Nodes: []storage.WorkflowNode{{
			ID: "n1", Type: "transformation",
			Config: map[string]any{
				"transType":      "ai_classify",
				"apiKey":         "NODEKEY-LEAK",
				"fallbackApiKey": "{{secret:OPENAI}}",
				"headers":        map[string]any{"Authorization": "Bearer HEADER-LEAK", "Accept": "application/json"},
				"clientSecret":   "CLIENT-LEAK",
				"maxTokens":      512,
			},
		}},
	}
	for name, got := range both(recordAudit(t, wf)) {
		for _, leak := range []string{"NODEKEY-LEAK", "HEADER-LEAK", "CLIENT-LEAK"} {
			if strings.Contains(got, leak) {
				t.Errorf("%s still holds %s: %s", name, leak, got)
			}
		}
		// A reference names a vhost secret; it is not one, and the trail is
		// more useful for showing which secret a node was pointed at.
		for _, kept := range []string{"{{secret:OPENAI}}", "application/json", "512", "ai_classify"} {
			if !strings.Contains(got, kept) {
				t.Errorf("%s lost the non-secret value %q: %s", name, kept, got)
			}
		}
	}
}

// Some callers hand over JSON they already marshalled.
func TestAuditTrailRedactsPayloadsGivenAsJSONText(t *testing.T) {
	for name, got := range both(recordAudit(t, `{"config":{"token":"TEXT-LEAK","topic":"orders"}}`)) {
		if strings.Contains(got, "TEXT-LEAK") {
			t.Errorf("%s still holds the token: %s", name, got)
		}
		if !strings.Contains(got, "orders") {
			t.Errorf("%s lost the topic: %s", name, got)
		}
	}
	// Text that is not JSON is a sentence written by the caller; keep it.
	for name, got := range both(recordAudit(t, "plain note")) {
		if got != "plain note" {
			t.Errorf("%s = %q, want the text unchanged", name, got)
		}
	}
}

func both(logData, auditPayload string) map[string]string {
	return map[string]string{"logs.data": logData, "audit_logs.payload": auditPayload}
}

func TestAuditSensitiveKeyReadsEverySpelling(t *testing.T) {
	cases := map[string]bool{
		"password": true, "dbPassword": true, "apiKey": true, "api_key": true, "APIKey": true,
		"X-Api-Key": true, "Authorization": true, "clientSecret": true, "privateKey": true,
		"accessToken": true, "webhookUrl": true, "Cookie": true,
		"partitionKey": false, "sortKey": false, "s3Key": false, "maxTokens": false,
		"host": false, "topic": false, "transType": false, "tokenizer": false,
	}
	for key, want := range cases {
		if got := isAuditSensitiveKey(key); got != want {
			t.Errorf("isAuditSensitiveKey(%q) = %v, want %v", key, got, want)
		}
	}
}
