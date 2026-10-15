// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package kafka

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
	"github.com/twmb/franz-go/pkg/kversion"
	"github.com/twmb/franz-go/pkg/sasl/plain"
)

// KafkaSink writes messages to one topic.
//
// Built with NewKafkaSink it delivers at-least-once. Built with
// NewTransactionalKafkaSink every batch is one Kafka transaction: its records
// become visible to read_committed consumers together or not at all, and when
// the batch came from a Kafka consumer group the group's offsets are committed
// in the same transaction. That is Kafka's consume-transform-produce
// exactly-once: after a crash the input is re-read from the last committed
// transaction, and whatever an unfinished one wrote is aborted, never seen.
type KafkaSink struct {
	topic           string
	formatter       hermod.Formatter
	transactionalID string

	// writeMu serialises batches: a producer has at most one open
	// transaction, so two concurrent batches must not interleave.
	writeMu sync.Mutex

	// mu guards cl only, and is never held across a request, so Ping and Close
	// do not wait behind a batch.
	mu      sync.Mutex
	cl      *kgo.Client
	initErr error
}

// NewKafkaSink builds an at-least-once Kafka sink.
func NewKafkaSink(brokers []string, topic string, username, password string, formatter hermod.Formatter) *KafkaSink {
	return newSink(brokers, topic, username, password, "", formatter)
}

// NewTransactionalKafkaSink builds a sink that writes each batch in one Kafka
// transaction under transactionalID.
//
// The ID is the producer's identity across restarts: a new producer with the
// same ID fences the old one and aborts whatever it left open. Two workflows
// must never share one, or each fences the other on every batch.
func NewTransactionalKafkaSink(brokers []string, topic, username, password, transactionalID string, formatter hermod.Formatter) *KafkaSink {
	return newSink(brokers, topic, username, password, transactionalID, formatter)
}

func newSink(brokers []string, topic, username, password, transactionalID string, formatter hermod.Formatter) *KafkaSink {
	opts := []kgo.Opt{
		kgo.SeedBrokers(brokers...),
		kgo.DefaultProduceTopic(topic),
		kgo.AllowAutoTopicCreation(),
	}
	if username != "" {
		opts = append(opts, kgo.SASL(plain.Auth{User: username, Pass: password}.AsMechanism()))
	}
	if transactionalID != "" {
		opts = append(opts,
			kgo.TransactionalID(transactionalID),
			// Offsets are committed into the transaction with explicit
			// AddOffsetsToTxn and TxnOffsetCommit requests. Their shape changed
			// with KIP-890 part 2 (Kafka 4.0's transaction.version 2: implicit
			// adds, a new epoch per transaction, topic IDs in TxnOffsetCommit
			// v6), and the client cannot be told which shape a hand-built
			// request is in. Capping the protocol at 3.9 keeps the client on
			// the classic transaction protocol, which every broker since 2.5
			// speaks — 4.x brokers included.
			kgo.MaxVersions(kversion.V3_9_0()),
		)
	}

	s := &KafkaSink{topic: topic, formatter: formatter, transactionalID: transactionalID}
	// NewClient does not dial; it fails only on contradictory options.
	s.cl, s.initErr = kgo.NewClient(opts...)
	return s
}

// TransactionalID reports the producer's transactional ID, empty for an
// at-least-once sink.
func (s *KafkaSink) TransactionalID() string { return s.transactionalID }

// NOTE: KafkaSink deliberately does NOT implement hermod.Transactional or
// hermod.TwoPhaseCommit, even with transactions on.
//
// Those interfaces let a coordinator prepare the sink and decide later. A
// Kafka transaction has no prepared state that survives the producer: if the
// process dies between "prepared" and "commit", the next producer with the
// same transactional ID aborts it. Claiming a durable prepare would let a
// coordinator commit its other participants on a promise Kafka cannot keep.
// The transaction here is per batch and self-contained instead.
// pkg/comm/sink/kafka/twopc_test.go guards this.

func (s *KafkaSink) client() (*kgo.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.initErr != nil {
		return nil, fmt.Errorf("kafka sink: %w", s.initErr)
	}
	if s.cl == nil {
		return nil, errors.New("kafka sink: closed")
	}
	return s.cl, nil
}

func (s *KafkaSink) Write(ctx context.Context, msg hermod.Message) error {
	return s.WriteBatch(ctx, []hermod.Message{msg})
}

func (s *KafkaSink) WriteBatch(ctx context.Context, msgs []hermod.Message) error {
	filtered := make([]hermod.Message, 0, len(msgs))
	for _, m := range msgs {
		if m != nil {
			filtered = append(filtered, m)
		}
	}
	msgs = filtered
	if len(msgs) == 0 {
		return nil
	}

	recs, err := s.records(msgs)
	if err != nil {
		return err
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	cl, err := s.client()
	if err != nil {
		return err
	}

	if s.transactionalID == "" {
		if err := cl.ProduceSync(ctx, recs...).FirstErr(); err != nil {
			return fmt.Errorf("failed to write batch to kafka: %w", err)
		}
		return nil
	}

	offsets, err := sourceOffsets(msgs)
	if err != nil {
		return fmt.Errorf("kafka sink: %w", err)
	}
	return s.transact(ctx, cl, recs, offsets)
}

// records formats a batch into Kafka records keyed by message ID.
func (s *KafkaSink) records(msgs []hermod.Message) ([]*kgo.Record, error) {
	recs := make([]*kgo.Record, len(msgs))
	for i, msg := range msgs {
		var data []byte
		var err error
		if s.formatter != nil {
			data, err = s.formatter.Format(msg)
		} else {
			data = msg.Payload()
		}
		if err != nil {
			return nil, fmt.Errorf("failed to format message %s: %w", msg.ID(), err)
		}
		r := &kgo.Record{Topic: s.topic, Value: data}
		// Left nil without an ID: an empty key is still hashed, which would put
		// every keyless record on the same partition.
		if id := msg.ID(); id != "" {
			r.Key = []byte(id)
		}
		recs[i] = r
	}
	return recs, nil
}

// transact writes one batch as one transaction. Any failure aborts it, so the
// batch is either entirely visible with its offsets committed, or not at all.
func (s *KafkaSink) transact(ctx context.Context, cl *kgo.Client, recs []*kgo.Record, offsets []groupOffsets) error {
	if err := cl.BeginTransaction(); err != nil {
		return fmt.Errorf("kafka sink: begin transaction: %w", err)
	}

	err := cl.ProduceSync(ctx, recs...).FirstErr()
	if err != nil {
		err = fmt.Errorf("failed to write batch to kafka: %w", err)
	}
	if err == nil {
		err = s.commitOffsets(ctx, cl, offsets)
	}
	if err == nil {
		if err := cl.EndTransaction(ctx, kgo.TryCommit); err != nil {
			return fmt.Errorf("kafka sink: commit transaction: %w", err)
		}
		return nil
	}

	// The abort gets its own context: the caller's may be what just expired,
	// and a transaction left open blocks read_committed consumers of these
	// partitions until the broker times it out.
	abortCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if abortErr := cl.AbortBufferedRecords(abortCtx); abortErr != nil {
		return errors.Join(err, fmt.Errorf("kafka sink: abort buffered records: %w", abortErr))
	}
	if abortErr := cl.EndTransaction(abortCtx, kgo.TryAbort); abortErr != nil {
		return errors.Join(err, fmt.Errorf("kafka sink: abort transaction: %w", abortErr))
	}
	return err
}

// commitOffsets adds the source consumer groups' offsets to the open
// transaction.
func (s *KafkaSink) commitOffsets(ctx context.Context, cl *kgo.Client, offsets []groupOffsets) error {
	if len(offsets) == 0 {
		return nil
	}
	pid, epoch, err := cl.ProducerID(ctx)
	if err != nil {
		return fmt.Errorf("kafka sink: producer id: %w", err)
	}
	for _, g := range offsets {
		if err := s.addOffsetsToTxn(ctx, cl, pid, epoch, g.group); err != nil {
			return fmt.Errorf("kafka sink: add group %s offsets to transaction: %w", g.group, err)
		}
		if err := s.txnOffsetCommit(ctx, cl, pid, epoch, g); err != nil {
			return fmt.Errorf("kafka sink: commit group %s offsets in transaction: %w", g.group, err)
		}
	}
	return nil
}

// addOffsetsToTxn registers the group's offsets partition with the
// transaction, so the coordinator writes its markers there too.
func (s *KafkaSink) addOffsetsToTxn(ctx context.Context, cl *kgo.Client, pid int64, epoch int16, group string) error {
	req := kmsg.NewPtrAddOffsetsToTxnRequest()
	req.TransactionalID = s.transactionalID
	req.ProducerID = pid
	req.ProducerEpoch = epoch
	req.Group = group
	return retryTxn(ctx, func() error {
		resp, err := req.RequestWith(ctx, cl)
		if err != nil {
			return err
		}
		return kerr.ErrorForCode(resp.ErrorCode)
	})
}

// txnOffsetCommit writes the group's offsets as part of the transaction.
func (s *KafkaSink) txnOffsetCommit(ctx context.Context, cl *kgo.Client, pid int64, epoch int16, g groupOffsets) error {
	req := kmsg.NewPtrTxnOffsetCommitRequest()
	req.TransactionalID = s.transactionalID
	req.Group = g.group
	req.ProducerID = pid
	req.ProducerEpoch = epoch
	// The member and generation that read the records. The coordinator
	// refuses the commit if the group has rebalanced since, which is what
	// stops a member that lost its partitions from committing over the
	// member that took them.
	req.Generation = g.generation
	req.MemberID = g.member
	for topic, parts := range g.offsets {
		rt := kmsg.NewTxnOffsetCommitRequestTopic()
		rt.Topic = topic
		for p, off := range parts {
			rp := kmsg.NewTxnOffsetCommitRequestTopicPartition()
			rp.Partition = p
			rp.Offset = off
			rp.LeaderEpoch = -1
			rt.Partitions = append(rt.Partitions, rp)
		}
		req.Topics = append(req.Topics, rt)
	}
	return retryTxn(ctx, func() error {
		resp, err := req.RequestWith(ctx, cl)
		if err != nil {
			return err
		}
		for _, t := range resp.Topics {
			for _, p := range t.Partitions {
				if err := kerr.ErrorForCode(p.ErrorCode); err != nil {
					return fmt.Errorf("%s/%d: %w", t.Topic, p.Partition, err)
				}
			}
		}
		return nil
	})
}

// retryTxn retries an offset request while the broker says to try again —
// CONCURRENT_TRANSACTIONS in particular, which a transaction coordinator
// returns briefly after every commit while it writes the markers.
func retryTxn(ctx context.Context, fn func() error) error {
	backoff := 20 * time.Millisecond
	for attempt := 0; ; attempt++ {
		err := fn()
		var ke *kerr.Error
		if err == nil || !errors.As(err, &ke) || !ke.Retriable || attempt == 9 {
			return err
		}
		select {
		case <-ctx.Done():
			return errors.Join(err, ctx.Err())
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, time.Second)
	}
}

// groupOffsets is what one consumer group member commits: the next offset to
// read per partition.
type groupOffsets struct {
	group      string
	member     string
	generation int32
	offsets    map[string]map[int32]int64
}

// sourceOffsets gathers the consumer group positions a batch carries in its
// metadata, as stamped by the Kafka source. Messages from anywhere else carry
// none and contribute nothing.
func sourceOffsets(msgs []hermod.Message) ([]groupOffsets, error) {
	var out []groupOffsets
	index := map[string]int{}

	for _, m := range msgs {
		group := m.Metadata()["kafka_group"]
		if group == "" {
			continue
		}
		pos, err := readPosition(m)
		if err != nil {
			return nil, err
		}
		i, ok := index[group]
		if !ok {
			i = len(out)
			index[group] = i
			out = append(out, groupOffsets{
				group: group, member: pos.member, generation: pos.generation,
				offsets: map[string]map[int32]int64{},
			})
		}
		g := &out[i]
		if g.member != pos.member || g.generation != pos.generation {
			return nil, fmt.Errorf("batch mixes group %s generation %d (member %s) with generation %d (member %s): "+
				"the group rebalanced mid-batch", group, g.generation, g.member, pos.generation, pos.member)
		}
		parts := g.offsets[pos.topic]
		if parts == nil {
			parts = map[int32]int64{}
			g.offsets[pos.topic] = parts
		}
		if next := pos.offset + 1; next > parts[pos.partition] {
			parts[pos.partition] = next
		}
	}
	return out, nil
}

// position is where a message was read, as the Kafka source stamped it.
type position struct {
	member     string
	generation int32
	topic      string
	partition  int32
	offset     int64
}

func readPosition(m hermod.Message) (position, error) {
	meta := m.Metadata()
	group := meta["kafka_group"]
	topic := meta["kafka_topic"]
	if topic == "" {
		return position{}, fmt.Errorf("message %s from group %s has no kafka_topic", m.ID(), group)
	}
	partition, err := strconv.ParseInt(meta["kafka_partition"], 10, 32)
	if err != nil {
		return position{}, fmt.Errorf("message %s: kafka_partition %q: %w", m.ID(), meta["kafka_partition"], err)
	}
	offset, err := strconv.ParseInt(meta["kafka_offset"], 10, 64)
	if err != nil {
		return position{}, fmt.Errorf("message %s: kafka_offset %q: %w", m.ID(), meta["kafka_offset"], err)
	}
	generation, err := strconv.ParseInt(meta["kafka_generation"], 10, 32)
	if err != nil {
		return position{}, fmt.Errorf("message %s: kafka_generation %q: %w", m.ID(), meta["kafka_generation"], err)
	}
	return position{
		member: meta["kafka_member_id"], generation: int32(generation),
		topic: topic, partition: int32(partition), offset: offset,
	}, nil
}

func (s *KafkaSink) Ping(ctx context.Context) error {
	cl, err := s.client()
	if err != nil {
		return err
	}
	// A metadata request for the topic, bounded by the caller's context: Ping
	// is the readiness path, called behind a probe timeout of a few seconds.
	req := kmsg.NewPtrMetadataRequest()
	req.AllowAutoTopicCreation = false
	rt := kmsg.NewMetadataRequestTopic()
	rt.Topic = kmsg.StringPtr(s.topic)
	req.Topics = append(req.Topics, rt)
	if _, err := req.RequestWith(ctx, cl); err != nil {
		return fmt.Errorf("failed to ping kafka: %w", err)
	}
	return nil
}

func (s *KafkaSink) Close() error {
	s.mu.Lock()
	cl := s.cl
	s.cl = nil
	s.mu.Unlock()
	if cl != nil {
		// Every batch is acknowledged by the broker, or its transaction ended,
		// before WriteBatch returns, so nothing buffered is lost here.
		cl.Close()
	}
	return nil
}
