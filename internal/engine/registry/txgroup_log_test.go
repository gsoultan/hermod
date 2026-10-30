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
	"path/filepath"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/factory"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/infra/state"
)

// TxGroupLogProblem is what the editor shows before anyone runs the workflow,
// and the same rule createTxGroupSink enforces when it does — so the form and
// the engine cannot disagree about whether a group can start.
func TestTxGroupLogProblem(t *testing.T) {
	memory, err := state.NewStateStore(state.Config{Type: "memory"})
	if err != nil {
		t.Fatal(err)
	}
	file, err := state.NewStateStore(state.Config{Type: "sqlite", Path: filepath.Join(t.TempDir(), "s.db")})
	if err != nil {
		t.Fatal(err)
	}

	none := &Registry{}
	if p := none.TxGroupLogProblem(); !strings.Contains(p, "state store") {
		t.Errorf("no state store: problem = %q, want one naming the state store", p)
	}

	inMemory := &Registry{}
	inMemory.SetStateStore(memory)
	if p := inMemory.TxGroupLogProblem(); !strings.Contains(p, "memory") {
		t.Errorf("in-memory state store: problem = %q, want one saying it is in memory", p)
	}

	durable := &Registry{}
	durable.SetStateStore(file)
	if p := durable.TxGroupLogProblem(); p != "" {
		t.Errorf("SQLite file state store: problem = %q, want none", p)
	}
}

// An in-memory log loses every in-doubt transaction on restart, which is the
// one thing the log exists to survive.
func TestTxGroupRefusesAnInMemoryStateStore(t *testing.T) {
	memory, err := state.NewStateStore(state.Config{Type: "memory"})
	if err != nil {
		t.Fatal(err)
	}
	r := &Registry{}
	r.SetStateStore(memory)

	_, err = r.createTxGroupSink(context.Background(), factory.SinkConfig{
		ID:     "grp",
		Type:   "txgroup",
		Config: map[string]string{"members": "a,b"},
	})
	if err == nil {
		t.Fatal("a group was created over an in-memory transaction log")
	}
	if !strings.Contains(err.Error(), "memory") {
		t.Errorf("the error should say the state store is in memory, got: %v", err)
	}
}

// "Test connection" on a group must work before the group is saved, which is
// when it has no ID — and it used to fail there with "an ID is required to
// scope its transaction log", the only answer it ever gave. It checks what
// would stop the group starting, and names the member at fault.
func TestCheckingAGroupNeedsNoID(t *testing.T) {
	r := registryWithSinks(map[string]storage.Sink{
		"a": postgresSink("a"),
		"k": {ID: "k", Name: "events", Type: "kafka", Config: map[string]string{"brokers": "127.0.0.1:1", "topic": "t"}},
	})

	err := r.TestSink(context.Background(), factory.SinkConfig{Type: "txgroup", Config: map[string]string{"members": "a,k"}})
	if err == nil {
		t.Fatal("a group with a Kafka member passed its check")
	}
	if strings.Contains(err.Error(), "ID is required") {
		t.Fatalf("checking an unsaved group still asks for an ID: %v", err)
	}
	for _, want := range []string{"events", "kafka"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error should name %q, got: %v", want, err)
		}
	}
}

func TestCheckingAGroupReportsWhatStopsItStarting(t *testing.T) {
	one := registryWithSinks(map[string]storage.Sink{"a": postgresSink("a")})
	if err := one.TestSink(context.Background(), factory.SinkConfig{Type: "txgroup", Config: map[string]string{"members": "a"}}); err == nil ||
		!strings.Contains(err.Error(), "at least two") {
		t.Errorf("one member: got %v, want it to ask for at least two", err)
	}

	noLog := registryWithSinks(map[string]storage.Sink{"a": postgresSink("a"), "b": postgresSink("b")})
	noLog.stateStore = nil
	if err := noLog.TestSink(context.Background(), factory.SinkConfig{Type: "txgroup", Config: map[string]string{"members": "a,b"}}); err == nil ||
		!strings.Contains(err.Error(), "state store") {
		t.Errorf("no state store: got %v, want it to name the state store", err)
	}

	badAge := registryWithSinks(map[string]storage.Sink{"a": postgresSink("a"), "b": postgresSink("b")})
	if err := badAge.TestSink(context.Background(), factory.SinkConfig{Type: "txgroup",
		Config: map[string]string{"members": "a,b", "max_prepared_age": "soon"}}); err == nil ||
		!strings.Contains(err.Error(), "max_prepared_age") {
		t.Errorf("bad max_prepared_age: got %v, want it named", err)
	}
}
