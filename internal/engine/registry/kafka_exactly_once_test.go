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
	"fmt"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
)

// Kafka to Kafka exactly-once and a second sink.
//
// A Kafka sink with a transactional ID commits the Kafka source's offsets in
// the same transaction as its own output. Those offsets then cover every sink
// in the workflow, not just the Kafka one: if a second sink fails, or the
// process dies before it writes, the restarted workflow resumes after records
// that sink never got. So a workflow that reads Kafka and turns exactly-once on
// may have no other sink, and it is refused when saved and when started.

type kafkaEOSStore struct {
	txGroupSinkStore
	sources map[string]storage.Source
}

func (s *kafkaEOSStore) GetSource(_ context.Context, id string) (storage.Source, error) {
	src, ok := s.sources[id]
	if !ok {
		return storage.Source{}, fmt.Errorf("no source %q", id)
	}
	return src, nil
}

func kafkaEOSRegistry() *Registry {
	kafka := func(id, txnID string) storage.Sink {
		cfg := map[string]string{"brokers": "127.0.0.1:1", "topic": "out"}
		if txnID != "" {
			cfg["transactional_id"] = txnID
		}
		return storage.Sink{ID: id, Type: "kafka", Config: cfg}
	}
	return &Registry{
		stateStore: newFakeStateStore(),
		storage: &kafkaEOSStore{
			txGroupSinkStore: txGroupSinkStore{sinks: map[string]storage.Sink{
				"eos":   kafka("eos", "hermod-orders"),
				"blank": kafka("blank", "   "),
				"plain": kafka("plain", ""),
				"pg":    postgresSink("pg"),
			}},
			sources: map[string]storage.Source{
				"kin": {ID: "kin", Type: "kafka", Config: map[string]string{"brokers": "127.0.0.1:1", "topic": "in", "group_id": "g"}},
				"cdc": {ID: "cdc", Type: "postgres", Config: map[string]string{"host": "127.0.0.1"}},
			},
		},
		sinkCache:   map[string]storage.Sink{},
		sourceCache: map[string]storage.Source{},
	}
}

func kafkaEOSWorkflow(source string, sinks ...string) storage.Workflow {
	wf := storage.Workflow{ID: "wf", Nodes: []storage.WorkflowNode{{ID: "S", Type: "source", RefID: source}}}
	for i, ref := range sinks {
		id := fmt.Sprintf("K%d", i+1)
		wf.Nodes = append(wf.Nodes, sinkNode(id, ref, ""))
		wf.Edges = append(wf.Edges, edges("S", id)...)
	}
	return wf
}

func TestKafkaExactlyOnceWithASecondSinkIsRefused(t *testing.T) {
	r := kafkaEOSRegistry()
	wf := kafkaEOSWorkflow("kin", "eos", "pg")

	err := r.CheckKafkaExactlyOnce(context.Background(), wf)
	if err == nil {
		t.Fatal("a Kafka source feeding a transactional Kafka sink and a PostgreSQL sink was accepted")
	}
	for _, want := range []string{"DB K1 (K1)", "hermod-orders", "DB K2 (K2)"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error should name %s, got: %v", want, err)
		}
	}

	// Starting runs the same check, so a workflow saved before it existed, or
	// written straight to the store, cannot run either.
	if err := r.ValidateWorkflow(context.Background(), wf); err == nil || !strings.Contains(err.Error(), "hermod-orders") {
		t.Errorf("ValidateWorkflow = %v, want the exactly-once refusal", err)
	}
}

// Two nodes on the same transactional sink are two writers sharing one
// transactional ID, each committing the offsets for the other: refused too.
func TestKafkaExactlyOnceSinkUsedTwiceIsRefused(t *testing.T) {
	if err := kafkaEOSRegistry().CheckKafkaExactlyOnce(context.Background(), kafkaEOSWorkflow("kin", "eos", "eos")); err == nil {
		t.Fatal("two nodes on one transactional Kafka sink were accepted")
	}
}

func TestKafkaExactlyOnceAcceptsWhatItCanHonour(t *testing.T) {
	cases := map[string]storage.Workflow{
		// The case the mode is for.
		"kafka to one transactional kafka sink": kafkaEOSWorkflow("kin", "eos"),
		// No transactional ID, or only spaces (the factory trims it), is an
		// ordinary at-least-once sink: the source's ack moves the offsets after
		// every sink has written.
		"no transactional id":    kafkaEOSWorkflow("kin", "plain", "pg"),
		"blank transactional id": kafkaEOSWorkflow("kin", "blank", "pg"),
		// Without a Kafka source there are no offsets in the transaction, so a
		// second sink loses nothing to it.
		"non-kafka source": kafkaEOSWorkflow("cdc", "eos", "pg"),
	}
	for name, wf := range cases {
		t.Run(name, func(t *testing.T) {
			if err := kafkaEOSRegistry().CheckKafkaExactlyOnce(context.Background(), wf); err != nil {
				t.Errorf("refused: %v", err)
			}
		})
	}
}

// A sink node no source reaches never runs, so it is not a second sink.
func TestKafkaExactlyOnceIgnoresAnUnconnectedSinkNode(t *testing.T) {
	wf := kafkaEOSWorkflow("kin", "eos")
	wf.Nodes = append(wf.Nodes, sinkNode("X", "pg", ""))
	if err := kafkaEOSRegistry().CheckKafkaExactlyOnce(context.Background(), wf); err != nil {
		t.Errorf("refused over a sink node nothing reaches: %v", err)
	}
}

// The workflow API's validator holds a registry that may be nil; there is
// nothing to look up then, and nothing to refuse.
func TestKafkaExactlyOnceOnANilRegistry(t *testing.T) {
	if err := (*Registry)(nil).CheckKafkaExactlyOnce(context.Background(), kafkaEOSWorkflow("kin", "eos", "pg")); err != nil {
		t.Errorf("a nil registry refused: %v", err)
	}
}
