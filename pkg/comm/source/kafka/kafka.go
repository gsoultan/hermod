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
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
	"github.com/twmb/franz-go/pkg/sasl/plain"
)

// KafkaSource consumes one topic as a member of a consumer group.
//
// Offsets are committed only when a message is acknowledged, never on a timer:
// a record the pipeline has not delivered must still be there after a restart.
// Reads are read_committed, so records from aborted or still-open transactions
// upstream are never delivered — on a topic nobody writes transactionally that
// changes nothing.
type KafkaSource struct {
	brokers  []string
	topic    string
	group    string
	username string
	password string

	// decoder, when set, reads Confluent-framed records. Nil means the
	// historical behaviour: try JSON, fall back to raw bytes.
	decoder recordDecoder

	mu      sync.Mutex
	cl      *kgo.Client
	initErr error
	commits *commitTracker

	// readMu serialises Read so the buffer of polled records has one owner. It
	// is separate from mu so Close never waits on a blocked poll.
	readMu sync.Mutex
	buf    []*kgo.Record
}

func NewKafkaSource(brokers []string, topic, groupID string, username, password string) *KafkaSource {
	s := &KafkaSource{
		brokers:  brokers,
		topic:    topic,
		group:    groupID,
		username: username,
		password: password,
		commits:  newCommitTracker(),
	}

	opts := append(baseOpts(brokers, username, password),
		kgo.ConsumeTopics(topic),
		kgo.FetchIsolationLevel(kgo.ReadCommitted()),
	)
	if groupID != "" {
		forget := func(_ context.Context, _ *kgo.Client, lost map[string][]int32) {
			s.commits.forget(lost)
		}
		opts = append(opts,
			kgo.ConsumerGroup(groupID),
			// A transactional sink commits this group's offsets inside its
			// transaction. franz-go always fetches offsets with RequireStable,
			// so a member taking over a partition waits for that transaction
			// to end instead of replaying what it is about to commit.
			kgo.DisableAutoCommit(),
			kgo.OnPartitionsRevoked(forget),
			kgo.OnPartitionsLost(forget),
		)
	}

	// NewClient does not dial; it fails only on contradictory options. The
	// error is kept and returned from the first call that needs the client,
	// because the constructor's signature predates it returning one.
	s.cl, s.initErr = kgo.NewClient(opts...)
	return s
}

// baseOpts are the connection settings shared by every client this package
// builds.
func baseOpts(brokers []string, username, password string) []kgo.Opt {
	opts := []kgo.Opt{kgo.SeedBrokers(brokers...)}
	if username != "" {
		opts = append(opts, kgo.SASL(plain.Auth{User: username, Pass: password}.AsMechanism()))
	}
	return opts
}

func (s *KafkaSource) client() (*kgo.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.initErr != nil {
		return nil, fmt.Errorf("kafka source: %w", s.initErr)
	}
	if s.cl == nil {
		return nil, errors.New("kafka source: closed")
	}
	return s.cl, nil
}

// recordDecoder reads a Confluent-framed record. It is an interface so the
// source does not depend on the registry client's construction.
type recordDecoder interface {
	Decode(ctx context.Context, data []byte) (map[string]any, error)
}

// SetDecoder makes the source read Confluent-framed records — the five-byte
// registry header plus an Avro body — instead of guessing at JSON.
//
// Once set, an unframed record is an error rather than something to fall back
// on. A topic that is not what the operator configured should say so, not
// quietly deliver bytes that will be wrong further downstream.
func (s *KafkaSource) SetDecoder(d recordDecoder) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.decoder = d
}

// buildMessage turns one record's bytes into a message.
//
// It is separate from Read so the decode path can be tested without a broker;
// Read supplies nothing but the bytes and the coordinates.
func (s *KafkaSource) buildMessage(
	ctx context.Context, value []byte, topic string, partition int, offset int64,
) (*message.DefaultMessage, error) {
	s.mu.Lock()
	dec := s.decoder
	s.mu.Unlock()

	msg := message.AcquireMessage()
	msg.SetPayload(value)

	switch {
	case dec != nil:
		// Configured for a registry topic: a record that does not decode is a
		// poison message, not something to deliver half-built.
		data, err := dec.Decode(ctx, value)
		if err != nil {
			msg.Release()
			return nil, fmt.Errorf("kafka source: topic %s partition %d offset %d: %w",
				topic, partition, offset, err)
		}
		for k, v := range data {
			msg.SetData(k, v)
		}

	default:
		// Try to unmarshal JSON into Data() for dynamic structure
		var jsonData map[string]any
		if err := json.Unmarshal(value, &jsonData); err == nil {
			for k, v := range jsonData {
				msg.SetData(k, v)
			}
		} else {
			msg.SetAfter(value) // Fallback for non-JSON
		}
	}

	msg.SetMetadata("kafka_topic", topic)
	msg.SetMetadata("kafka_partition", strconv.Itoa(partition))
	msg.SetMetadata("kafka_offset", strconv.FormatInt(offset, 10))
	return msg, nil
}

// Metadata keys a record carries besides its coordinates. A transactional
// sink reads them to commit this record's offset as the consumer group that
// read it, in the same transaction as its output.
const (
	MetaGroup      = "kafka_group"
	MetaMemberID   = "kafka_member_id"
	MetaGeneration = "kafka_generation"
)

// stampGroup records which consumer group member read a message. A groupless
// consumer stamps nothing: there is no group offset for anyone to commit.
func stampGroup(msg *message.DefaultMessage, group, member string, generation int32) {
	if group == "" {
		return
	}
	msg.SetMetadata(MetaGroup, group)
	msg.SetMetadata(MetaMemberID, member)
	msg.SetMetadata(MetaGeneration, strconv.FormatInt(int64(generation), 10))
}

func (s *KafkaSource) Read(ctx context.Context) (hermod.Message, error) {
	s.readMu.Lock()
	defer s.readMu.Unlock()

	for len(s.buf) == 0 {
		cl, err := s.client()
		if err != nil {
			return nil, err
		}
		fetches := cl.PollFetches(ctx)
		if fetches.IsClientClosed() {
			return nil, errors.New("kafka source: closed")
		}
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("failed to fetch message from kafka: %w", err)
		}
		if errs := fetches.Errors(); len(errs) > 0 {
			e := errs[0]
			return nil, fmt.Errorf("failed to fetch message from kafka: topic %s partition %d: %w",
				e.Topic, e.Partition, e.Err)
		}
		s.buf = fetches.Records()
	}

	r := s.buf[0]
	s.buf[0] = nil
	s.buf = s.buf[1:]

	msg, err := s.buildMessage(ctx, r.Value, r.Topic, int(r.Partition), r.Offset)
	if err != nil {
		return nil, err
	}
	msg.SetID(string(r.Key))
	if cl, err := s.client(); err == nil {
		member, generation := cl.GroupMetadata()
		stampGroup(msg, s.group, member, generation)
	}
	return msg, nil
}

func (s *KafkaSource) Ack(ctx context.Context, msg hermod.Message) error {
	// A nil message must surface as an error rather than a dereference. The
	// engine acknowledges on the hot path, so a crash — or, as this actually
	// behaved, a goroutine that never returns — takes the consumer with it.
	if msg == nil {
		return errors.New("kafka source: nil message")
	}

	meta := msg.Metadata()
	topic := meta["kafka_topic"]
	partitionStr := meta["kafka_partition"]
	offsetStr := meta["kafka_offset"]
	if topic == "" || partitionStr == "" || offsetStr == "" {
		return errors.New("missing kafka metadata in message")
	}
	partition, err := strconv.ParseInt(partitionStr, 10, 32)
	if err != nil {
		return fmt.Errorf("kafka source: partition %q: %w", partitionStr, err)
	}
	offset, err := strconv.ParseInt(offsetStr, 10, 64)
	if err != nil {
		return fmt.Errorf("kafka source: offset %q: %w", offsetStr, err)
	}

	// Without a group there is no stored position to move.
	if s.group == "" {
		return nil
	}

	cl, err := s.client()
	if err != nil {
		return err
	}
	next, ok := s.commits.advance(topic, int32(partition), offset)
	if !ok {
		return nil
	}

	var commitErr error
	cl.CommitOffsetsSync(ctx, map[string]map[int32]kgo.EpochOffset{
		topic: {int32(partition): {Epoch: -1, Offset: next}},
	}, func(_ *kgo.Client, _ *kmsg.OffsetCommitRequest, resp *kmsg.OffsetCommitResponse, err error) {
		if err != nil {
			commitErr = err
			return
		}
		for _, t := range resp.Topics {
			for _, p := range t.Partitions {
				if err := kerr.ErrorForCode(p.ErrorCode); err != nil && commitErr == nil {
					commitErr = err
				}
			}
		}
	})
	if commitErr != nil {
		// The commit did not land, so the tracker must not believe it did.
		s.commits.forget(map[string][]int32{topic: {int32(partition)}})
		return fmt.Errorf("failed to commit kafka offset: %w", commitErr)
	}
	return nil
}

func (s *KafkaSource) IsReady(ctx context.Context) error {
	if err := s.Ping(ctx); err != nil {
		return fmt.Errorf("kafka connection failed: %w", err)
	}
	cl, err := s.client()
	if err != nil {
		return err
	}
	parts, err := topicPartitions(ctx, cl, s.topic)
	if err != nil {
		return fmt.Errorf("failed to read partitions for topic '%s': %w. Ensure topic exists and user has permissions", s.topic, err)
	}
	if parts == 0 {
		return fmt.Errorf("kafka topic '%s' has no partitions", s.topic)
	}
	return nil
}

// topicPartitions asks the cluster how many partitions a topic has, without
// asking it to create the topic.
func topicPartitions(ctx context.Context, cl *kgo.Client, topic string) (int, error) {
	req := kmsg.NewPtrMetadataRequest()
	req.AllowAutoTopicCreation = false
	rt := kmsg.NewMetadataRequestTopic()
	rt.Topic = kmsg.StringPtr(topic)
	req.Topics = append(req.Topics, rt)

	resp, err := req.RequestWith(ctx, cl)
	if err != nil {
		return 0, err
	}
	for _, t := range resp.Topics {
		if t.Topic != nil && *t.Topic == topic {
			if err := kerr.ErrorForCode(t.ErrorCode); err != nil {
				return 0, err
			}
			return len(t.Partitions), nil
		}
	}
	return 0, kerr.UnknownTopicOrPartition
}

func (s *KafkaSource) Ping(ctx context.Context) error {
	cl, err := s.client()
	if err != nil {
		return err
	}
	if err := cl.Ping(ctx); err != nil {
		return fmt.Errorf("kafka ping failed for brokers %v: %w", s.brokers, err)
	}
	return nil
}

// Sample reads one record from the start of the topic without joining a
// group, so it moves no one's offsets and triggers no rebalance.
func (s *KafkaSource) Sample(ctx context.Context, table string) (hermod.Message, error) {
	opts := append(baseOpts(s.brokers, s.username, s.password),
		kgo.ConsumeTopics(s.topic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
		kgo.FetchIsolationLevel(kgo.ReadCommitted()),
	)
	cl, err := kgo.NewClient(opts...)
	if err != nil {
		return nil, fmt.Errorf("kafka source: %w", err)
	}
	defer cl.Close()

	// A timeout so an empty topic does not block forever.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	for {
		fetches := cl.PollRecords(ctx, 1)
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("failed to fetch message from kafka: %w", err)
		}
		if errs := fetches.Errors(); len(errs) > 0 {
			return nil, fmt.Errorf("failed to fetch message from kafka: %w", errs[0].Err)
		}
		if recs := fetches.Records(); len(recs) > 0 {
			r := recs[0]
			msg, err := s.buildMessage(ctx, r.Value, r.Topic, int(r.Partition), r.Offset)
			if err != nil {
				return nil, err
			}
			msg.SetID(string(r.Key))
			return msg, nil
		}
	}
}

func (s *KafkaSource) Close() error {
	s.mu.Lock()
	cl := s.cl
	s.cl = nil
	s.mu.Unlock()
	if cl != nil {
		// Close leaves the group, so the partitions move to another member
		// now rather than after the session timeout.
		cl.Close()
	}
	return nil
}

// commitTracker remembers the highest offset committed per partition, so an
// acknowledgement that arrives late never moves the group backwards.
type commitTracker struct {
	mu   sync.Mutex
	next map[string]map[int32]int64
}

func newCommitTracker() *commitTracker {
	return &commitTracker{next: map[string]map[int32]int64{}}
}

// advance reports the offset to commit after acknowledging offset, and false
// when an equal or later position is already committed.
func (c *commitTracker) advance(topic string, partition int32, offset int64) (int64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	next := offset + 1
	parts := c.next[topic]
	if parts == nil {
		parts = map[int32]int64{}
		c.next[topic] = parts
	}
	if cur, ok := parts[partition]; ok && cur >= next {
		return 0, false
	}
	parts[partition] = next
	return next, true
}

// forget drops what is known about partitions this member no longer owns.
func (c *commitTracker) forget(lost map[string][]int32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for topic, parts := range lost {
		for _, p := range parts {
			delete(c.next[topic], p)
		}
	}
}
