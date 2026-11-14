// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package factory

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/comm/sink/txsql"
)

func TestCreateSinkBuildsATransactionalSQLSink(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orders.db")
	cfg := SinkConfig{ID: "tx", Type: "txsql", Config: map[string]string{
		"driver": "sqlite",
		"path":   path,
		"statements": `[
			{"name":"order","sql":"INSERT INTO orders (ref) VALUES ({{ref}}) RETURNING id"},
			{"name":"line","sql":"INSERT INTO lines (order_id) VALUES ({{order.id}})"}
		]`,
	}}

	base, err := createSinkBase(cfg)
	if err != nil {
		t.Fatalf("createSinkBase: %v", err)
	}
	snk, ok := base.(*txsql.Sink)
	if !ok {
		t.Fatalf("createSinkBase(txsql) = %T, want *txsql.Sink", base)
	}
	t.Cleanup(func() { _ = snk.Close() })

	// The full path the engine uses, decorators included.
	decorated, err := CreateSink(cfg)
	if err != nil {
		t.Fatalf("CreateSink: %v", err)
	}
	t.Cleanup(func() { _ = decorated.Close() })
	if _, ok := decorated.(hermod.BatchSink); !ok {
		t.Fatalf("decorated txsql sink %T lost WriteBatch", decorated)
	}

	ctx := context.Background()
	if err := decorated.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	msg := message.AcquireMessage()
	defer msg.Release()
	msg.SetID("m1")
	msg.SetData("ref", "R-1")
	err = decorated.Write(ctx, msg)
	if err == nil || !strings.Contains(err.Error(), "no such table") {
		t.Fatalf("Write before the tables exist = %v, want the database's own refusal", err)
	}
}

func TestCreateSinkRefusesABadTransactionalSQLConfig(t *testing.T) {
	_, err := CreateSink(SinkConfig{ID: "tx", Type: "txsql", Config: map[string]string{
		"driver":     "sqlite",
		"path":       filepath.Join(t.TempDir(), "x.db"),
		"statements": `[{"name":"a","sql":"UPDATE t SET x = {{b.id}}"},{"name":"b","sql":"INSERT INTO t (y) VALUES (1) RETURNING id"}]`,
	}})
	if err == nil || !strings.Contains(err.Error(), "runs later") {
		t.Fatalf("CreateSink = %v, want the forward reference refused", err)
	}
}

func TestCreateSinkRefusesATransactionalSQLSinkWithNoConnection(t *testing.T) {
	_, err := CreateSink(SinkConfig{ID: "tx", Type: "txsql", Config: map[string]string{
		"driver":     "postgres",
		"statements": `[{"sql":"UPDATE t SET x = 1"}]`,
	}})
	if err == nil {
		t.Fatal("CreateSink accepted a sink with no connection details")
	}
}
