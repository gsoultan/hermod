// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

//go:build integration

package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/hermod/pkg/comm/message"
	sinkkafka "github.com/gsoultan/hermod/pkg/comm/sink/kafka"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
)

// The Kafka data path, against a real broker.
//
// Kafka is Beta, which in this repository means substantial and unit-tested but
// never proven to move a record. It is also one of the connectors people reach
// for first, so "probably fine" is a weaker position than it sounds — every
// other connector taken from that state to a live test this week turned out to
// have something wrong with it.
//
// Two properties are worth the broker. That a record written by the sink comes
// back out of the source intact, and that acknowledging it moves the consumer
// group's offset — because an offset that does not move means a restart
// redelivers everything, and an offset that moves too early means a restart
// skips what was never delivered.
//
// Run with:
//
//	HERMOD_INTEGRATION=1 KAFKA_BROKERS=127.0.0.1:9092 \
//	go test -tags=integration ./pkg/comm/source/kafka/

func requireKafka(t *testing.T) ([]string, string) {
	t.Helper()
	raw := os.Getenv("KAFKA_BROKERS")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || raw == "" {
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			t.Fatalf("HERMOD_INTEGRATION=%q KAFKA_BROKERS=%q in CI, where a broker is started "+
				"for exactly this", os.Getenv("HERMOD_INTEGRATION"), raw)
		}
		t.Skip("integration: set HERMOD_INTEGRATION=1 and KAFKA_BROKERS to run")
	}
	brokers := strings.Split(raw, ",")
	return brokers, createTopic(t, brokers, 1)
}

// createTopic makes a fresh topic and waits until it can take writes.
//
// Created explicitly rather than left to auto-creation, so a broker with
// auto-creation off behaves the same and the partition count is known.
func createTopic(t *testing.T, brokers []string, partitions int32) string {
	t.Helper()
	topic := fmt.Sprintf("hermod_it_%d", time.Now().UnixNano())

	cl := adminClient(t, brokers)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	req := kmsg.NewPtrCreateTopicsRequest()
	rt := kmsg.NewCreateTopicsRequestTopic()
	rt.Topic = topic
	rt.NumPartitions = partitions
	rt.ReplicationFactor = 1
	req.Topics = append(req.Topics, rt)
	req.TimeoutMillis = 30_000
	resp, err := req.RequestWith(ctx, cl)
	if err != nil {
		t.Fatalf("creating topic %s: %v", topic, err)
	}
	if err := kerr.ErrorForCode(resp.Topics[0].ErrorCode); err != nil {
		t.Fatalf("creating topic %s: %v", topic, err)
	}
	t.Cleanup(func() {
		del := kmsg.NewPtrDeleteTopicsRequest()
		dt := kmsg.NewDeleteTopicsRequestTopic()
		dt.Topic = kmsg.StringPtr(topic)
		del.Topics = append(del.Topics, dt)
		del.TopicNames = []string{topic}
		_, _ = del.RequestWith(context.Background(), cl)
	})

	waitForTopic(t, cl, topic, partitions)
	return topic
}

func adminClient(t *testing.T, brokers []string) *kgo.Client {
	t.Helper()
	cl, err := kgo.NewClient(kgo.SeedBrokers(brokers...))
	if err != nil {
		t.Fatalf("admin client: %v", err)
	}
	t.Cleanup(cl.Close)
	return cl
}

// waitForTopic blocks until the topic is actually usable, which is not the same
// as CreateTopics having returned.
//
// CreateTopics completes when the controller has accepted the request. The
// topic still has to appear in the metadata the producer's broker serves, and a
// partition leader still has to be elected. Producing inside that window fails
// with UNKNOWN_TOPIC_OR_PARTITION, and the failure belongs to the test setup
// rather than to anything the sink did.
//
// This is not hypothetical: it is how these tests failed in CI, on one of two
// runs of the same commit — which is what a race looks like from the outside,
// and why the local run and the other CI run both stayed green.
func waitForTopic(t *testing.T, cl *kgo.Client, topic string, partitions int32) {
	t.Helper()

	deadline := time.Now().Add(30 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		req := kmsg.NewPtrMetadataRequest()
		mt := kmsg.NewMetadataRequestTopic()
		mt.Topic = kmsg.StringPtr(topic)
		req.Topics = append(req.Topics, mt)
		resp, err := req.RequestWith(t.Context(), cl)

		switch {
		case err != nil:
			last = err
		case len(resp.Topics) == 0:
			last = errors.New("topic not in metadata")
		case kerr.ErrorForCode(resp.Topics[0].ErrorCode) != nil:
			last = kerr.ErrorForCode(resp.Topics[0].ErrorCode)
		case int32(len(resp.Topics[0].Partitions)) < partitions:
			last = fmt.Errorf("%d of %d partitions reported", len(resp.Topics[0].Partitions), partitions)
		default:
			// A partition with no leader cannot accept a write yet.
			leaderless := 0
			for _, p := range resp.Topics[0].Partitions {
				if p.Leader < 0 {
					leaderless++
				}
			}
			if leaderless == 0 {
				return
			}
			last = fmt.Errorf("%d of %d partition(s) have no leader", leaderless, partitions)
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("topic %s was created but never became usable within 30s: %v", topic, last)
}

// produce writes n messages through the sink, which is the path a workflow uses.
func produce(t *testing.T, brokers []string, topic string, n int) {
	t.Helper()
	snk := sinkkafka.NewKafkaSink(brokers, topic, "", "", nil)
	t.Cleanup(func() { _ = snk.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	for i := range n {
		msg := message.AcquireMessage()
		msg.SetID(fmt.Sprintf("key-%d", i))
		body, _ := json.Marshal(map[string]any{"seq": i, "name": "ada"})
		msg.SetPayload(body)
		if err := snk.Write(ctx, msg); err != nil {
			t.Fatalf("producing %d: %v", i, err)
		}
		msg.Release()
	}
}

// A record written by the sink must come back out of the source intact.
func TestARecordWrittenBySinkComesBackOutOfTheSource(t *testing.T) {
	brokers, topic := requireKafka(t)
	const total = 5
	produce(t, brokers, topic, total)

	src := NewKafkaSource(brokers, topic, "grp_"+topic, "", "")
	t.Cleanup(func() { _ = src.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()

	seen := map[int]string{}
	for len(seen) < total {
		msg, err := src.Read(ctx)
		if err != nil {
			t.Fatalf("read after %d of %d: %v", len(seen), total, err)
		}
		if msg == nil {
			continue
		}
		seq, ok := msg.Data()["seq"].(float64)
		if !ok {
			t.Fatalf("message carries no seq field; data=%v payload=%q", msg.Data(), msg.Payload())
		}
		seen[int(seq)] = msg.ID()
		if err := src.Ack(ctx, msg); err != nil {
			t.Fatalf("ack: %v", err)
		}
	}

	for i := range total {
		if id, ok := seen[i]; !ok {
			t.Errorf("record seq=%d never arrived", i)
		} else if id != fmt.Sprintf("key-%d", i) {
			t.Errorf("record seq=%d came back with id %q, want %q — the sink writes the "+
				"message id as the Kafka key and the source reads it back, so losing it "+
				"costs sink-side idempotency its dedup key", i, id, fmt.Sprintf("key-%d", i))
		}
	}
}

// Acknowledging must move the consumer group's offset, or a restart replays
// everything that was already delivered.
func TestAcknowledgedRecordsAreNotRedeliveredToTheSameGroup(t *testing.T) {
	brokers, topic := requireKafka(t)
	group := "grp_" + topic
	const total = 4
	produce(t, brokers, topic, total)

	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()

	first := NewKafkaSource(brokers, topic, group, "", "")
	for i := range total {
		msg, err := first.Read(ctx)
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		if err := first.Ack(ctx, msg); err != nil {
			t.Fatalf("ack %d: %v", i, err)
		}
	}
	if err := first.Close(); err != nil {
		t.Fatalf("closing the first consumer: %v", err)
	}

	// One new record, so there is something to prove the replacement consumer
	// is live rather than merely silent.
	produce(t, brokers, topic, 1)

	second := NewKafkaSource(brokers, topic, group, "", "")
	t.Cleanup(func() { _ = second.Close() })

	readCtx, readCancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer readCancel()

	msg, err := second.Read(readCtx)
	if err != nil {
		t.Fatalf("the replacement consumer read nothing: %v", err)
	}
	seq, _ := msg.Data()["seq"].(float64)
	if int(seq) != 0 {
		// produce() restarts numbering, so the new record is seq=0. Anything
		// from the first batch means the offset never moved.
		t.Errorf("after acknowledging %d records the replacement consumer was handed seq=%d\n"+
			"the committed offset did not advance, so every restart replays what was "+
			"already delivered", total, int(seq))
	}
}
