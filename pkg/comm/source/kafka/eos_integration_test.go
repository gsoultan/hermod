// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

//go:build integration

package kafka

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	sinkkafka "github.com/gsoultan/hermod/pkg/comm/sink/kafka"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
)

// Kafka to Kafka exactly-once, against a real broker.
//
// The claim is the one Kafka's transactions make: a record read from the input
// appears in the output once, however the pipeline dies, because the output
// and the input offset are committed in one transaction. So the test kills the
// pipeline in the two places that matter — after writing output but before
// committing it, and after the group rebalanced away from the member holding a
// batch — and then reads the output the way a downstream consumer would.
func TestKafkaToKafkaDeliversEachRecordExactlyOnceAcrossACrash(t *testing.T) {
	brokers, input := requireKafka(t)
	output := createTopic(t, brokers, 1)
	group := "grp_" + input
	txnID := "hermod-it-" + input
	const total = 10
	produce(t, brokers, input, total)

	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	defer cancel()

	// First run: the first half goes through cleanly. The source is never
	// acknowledged, so the only thing that can move the group's offset is the
	// sink's transaction.
	first := NewKafkaSource(brokers, input, group, "", "")
	sink := sinkkafka.NewTransactionalKafkaSink(brokers, output, "", "", txnID, nil)
	if err := sink.WriteBatch(ctx, readN(ctx, t, first, 5)); err != nil {
		t.Fatalf("first transactional batch: %v", err)
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}

	// Then the crash: the second half is read and written, but the process
	// dies before the transaction commits. The zombie producer below holds that
	// open transaction exactly as a killed process would leave it.
	stale := readN(ctx, t, first, 5)
	zombie, err := kgo.NewClient(kgo.SeedBrokers(brokers...), kgo.TransactionalID(txnID),
		kgo.DefaultProduceTopic(output))
	if err != nil {
		t.Fatal(err)
	}
	defer zombie.Close()
	if err := zombie.BeginTransaction(); err != nil {
		t.Fatal(err)
	}
	for _, m := range stale {
		if err := zombie.ProduceSync(ctx, &kgo.Record{Key: []byte(m.ID()), Value: m.Payload()}).FirstErr(); err != nil {
			t.Fatalf("zombie produce: %v", err)
		}
	}
	// The consumer dies too: closing it leaves the group, which rebalances.
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	// Restart. A new sink with the same transactional ID fences the zombie and
	// aborts what it left open.
	second := NewKafkaSource(brokers, input, group, "", "")
	t.Cleanup(func() { _ = second.Close() })
	restarted := sinkkafka.NewTransactionalKafkaSink(brokers, output, "", "", txnID, nil)
	t.Cleanup(func() { _ = restarted.Close() })

	replayed := readN(ctx, t, second, 5)
	if got := seqs(t, replayed); fmt.Sprint(got) != "[5 6 7 8 9]" {
		t.Fatalf("after the restart the group resumed at %v, want [5 6 7 8 9]: the offset the "+
			"first transaction committed is not where the group restarted", got)
	}

	// A batch still stamped with the old member's generation must be refused:
	// that member lost its partitions, and committing its offsets would write
	// over whoever owns them now.
	if err := restarted.WriteBatch(ctx, stale); err == nil {
		t.Fatal("a batch from a member the group rebalanced away from was committed")
	}

	if err := restarted.WriteBatch(ctx, replayed); err != nil {
		t.Fatalf("replayed batch: %v", err)
	}

	// The zombie cannot commit: it was fenced.
	if err := zombie.EndTransaction(ctx, kgo.TryCommit); err == nil {
		t.Fatal("the fenced producer committed its transaction")
	}

	got := readCommitted(ctx, t, brokers, output, total)
	if fmt.Sprint(got) != "[0 1 2 3 4 5 6 7 8 9]" {
		t.Fatalf("a read_committed consumer of the output saw %v, want each of 0..9 once", got)
	}
	if off := committedOffset(ctx, t, brokers, group, input); off != total {
		t.Fatalf("group %s committed offset %d on %s, want %d", group, off, input, total)
	}
}

func readN(ctx context.Context, t *testing.T, src *KafkaSource, n int) []hermod.Message {
	t.Helper()
	out := make([]hermod.Message, 0, n)
	for len(out) < n {
		m, err := src.Read(ctx)
		if err != nil {
			t.Fatalf("read %d of %d: %v", len(out), n, err)
		}
		out = append(out, m)
	}
	return out
}

func seqs(t *testing.T, msgs []hermod.Message) []int {
	t.Helper()
	out := make([]int, 0, len(msgs))
	for _, m := range msgs {
		seq, ok := m.Data()["seq"].(float64)
		if !ok {
			t.Fatalf("message without seq: %v", m.Data())
		}
		out = append(out, int(seq))
	}
	return out
}

// readCommitted reads the topic from the start as a read_committed consumer
// would, and keeps reading for a moment after it has seen want records, so a
// duplicate arriving late is caught rather than missed.
func readCommitted(ctx context.Context, t *testing.T, brokers []string, topic string, want int) []int {
	t.Helper()
	cl, err := kgo.NewClient(kgo.SeedBrokers(brokers...), kgo.ConsumeTopics(topic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
		kgo.FetchIsolationLevel(kgo.ReadCommitted()))
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()

	var got []int
	quiet := time.Time{}
	for {
		pollCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		fetches := cl.PollFetches(pollCtx)
		cancel()
		for _, r := range fetches.Records() {
			var body map[string]any
			if err := json.Unmarshal(r.Value, &body); err != nil {
				t.Fatalf("output record %q: %v", r.Value, err)
			}
			got = append(got, int(body["seq"].(float64)))
		}
		if len(got) >= want {
			if quiet.IsZero() {
				quiet = time.Now().Add(3 * time.Second)
			} else if time.Now().After(quiet) {
				break
			}
		}
		if ctx.Err() != nil {
			break
		}
	}
	sort.Ints(got)
	return got
}

func committedOffset(ctx context.Context, t *testing.T, brokers []string, group, topic string) int64 {
	t.Helper()
	cl := adminClient(t, brokers)
	req := kmsg.NewPtrOffsetFetchRequest()
	req.Group = group
	rt := kmsg.NewOffsetFetchRequestTopic()
	rt.Topic = topic
	rt.Partitions = []int32{0}
	req.Topics = append(req.Topics, rt)
	req.RequireStable = true
	resp, err := req.RequestWith(ctx, cl)
	if err != nil {
		t.Fatal(err)
	}
	if err := kerr.ErrorForCode(resp.ErrorCode); err != nil {
		t.Fatal(err)
	}
	if len(resp.Topics) == 1 && len(resp.Topics[0].Partitions) == 1 {
		p := resp.Topics[0].Partitions[0]
		if err := kerr.ErrorForCode(p.ErrorCode); err != nil {
			t.Fatal(err)
		}
		return p.Offset
	}
	t.Fatal("no committed offset returned")
	return 0
}
