// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
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
	"testing"

	"github.com/gsoultan/hermod/pkg/comm/message"
)

// Acknowledgements can arrive out of order across a batch. A commit for an
// older record after a newer one must not move the group's offset backwards,
// or a restart replays records that were already delivered.
func TestAnOlderAckDoesNotMoveTheCommittedOffsetBackwards(t *testing.T) {
	c := newCommitTracker()

	if next, ok := c.advance("orders", 0, 9); !ok || next != 10 {
		t.Fatalf("first ack: next=%d ok=%v, want 10 true", next, ok)
	}
	if _, ok := c.advance("orders", 0, 4); ok {
		t.Fatal("an older ack would have moved the offset backwards")
	}
	if next, ok := c.advance("orders", 0, 10); !ok || next != 11 {
		t.Fatalf("newer ack: next=%d ok=%v, want 11 true", next, ok)
	}
	// Partitions are independent.
	if next, ok := c.advance("orders", 1, 0); !ok || next != 1 {
		t.Fatalf("other partition: next=%d ok=%v, want 1 true", next, ok)
	}
}

// After a partition is revoked another member may commit it, or an operator
// may reset it. The tracker must forget what it knew, or it would refuse the
// legitimate commits that follow when the partition comes back.
func TestForgettingAPartitionAcceptsLowerOffsetsAgain(t *testing.T) {
	c := newCommitTracker()
	c.advance("orders", 0, 100)
	c.forget(map[string][]int32{"orders": {0}})
	if next, ok := c.advance("orders", 0, 5); !ok || next != 6 {
		t.Fatalf("after forget: next=%d ok=%v, want 6 true", next, ok)
	}
}

// The group identity travels with each record so a transactional sink can
// commit the offset in the same transaction as its output.
func TestRecordsCarryTheirConsumerGroupIdentity(t *testing.T) {
	s := &KafkaSource{group: "grp"}
	msg, err := s.buildMessage(context.Background(), []byte(`{"a":1}`), "orders", 2, 41)
	if err != nil {
		t.Fatal(err)
	}
	defer msg.Release()
	stampGroup(msg, "grp", "member-1", 7)

	want := map[string]string{
		"kafka_topic": "orders", "kafka_partition": "2", "kafka_offset": "41",
		"kafka_group": "grp", "kafka_member_id": "member-1", "kafka_generation": "7",
	}
	for k, v := range want {
		if got := msg.Metadata()[k]; got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
}

// A source with no group has nothing to commit and must not stamp a group a
// sink would then try to commit for.
func TestRecordsWithoutAGroupCarryNoGroupIdentity(t *testing.T) {
	m := message.AcquireMessage()
	defer m.Release()
	stampGroup(m, "", "", -1)
	if _, ok := m.Metadata()["kafka_group"]; ok {
		t.Fatal("kafka_group set for a groupless consumer")
	}
}
