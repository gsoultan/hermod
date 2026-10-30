// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package txgroup

import (
	"errors"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// Each member gets only its own rows.
//
// A group built from the sink nodes of a workflow has members that are those
// nodes, and a row that came through node A is for database A alone. The row
// says which node it came through (hermod.MetaSinkNode); the group hands it to
// that member and to no other. A row that names no node, or a node that is not
// a member, is for every member: that is what the stand-alone Transaction
// Group node has always meant, and it keeps meaning it.

func rowFor(t *testing.T, id, node string) hermod.Message {
	t.Helper()
	m := message.AcquireMessage()
	m.SetID(id)
	if node != "" {
		m.SetMetadata(hermod.MetaSinkNode, node)
	}
	return m
}

func TestARowGoesOnlyToTheMemberItWasBuiltFor(t *testing.T) {
	a := &txSink{name: "a"}
	b := &txSink{name: "b"}
	g := newGroup(t, Member{ID: "node-a", Sink: a}, Member{ID: "node-b", Sink: b})

	err := g.WriteBatch(t.Context(), []hermod.Message{
		rowFor(t, "r1", "node-a"),
		rowFor(t, "r2", "node-b"),
		rowFor(t, "r3", "node-a"),
	})
	if err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}
	if w, c, _ := a.counts(); w != 2 || c != 1 {
		t.Errorf("member a: written %d, committed %d; want 2 rows in 1 commit", w, c)
	}
	if w, c, _ := b.counts(); w != 1 || c != 1 {
		t.Errorf("member b: written %d, committed %d; want 1 row in 1 commit", w, c)
	}
}

func TestARowThatNamesNoNodeGoesToEveryMember(t *testing.T) {
	a := &txSink{name: "a"}
	b := &txSink{name: "b"}
	g := newGroup(t, Member{ID: "a", Sink: a}, Member{ID: "b", Sink: b})

	if err := g.WriteBatch(t.Context(), []hermod.Message{rowFor(t, "r1", ""), rowFor(t, "r2", "not-a-member")}); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}
	if w, _, _ := a.counts(); w != 2 {
		t.Errorf("member a written %d, want both rows", w)
	}
	if w, _, _ := b.counts(); w != 2 {
		t.Errorf("member b written %d, want both rows", w)
	}
}

func TestAMemberWithNoRowsInTheBatchIsLeftAlone(t *testing.T) {
	a := &txSink{name: "a"}
	b := &txSink{name: "b"}
	g := newGroup(t, Member{ID: "node-a", Sink: a}, Member{ID: "node-b", Sink: b})

	if err := g.WriteBatch(t.Context(), []hermod.Message{rowFor(t, "r1", "node-a")}); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}
	if w, _, rb := a.counts(); w != 1 || rb != 0 {
		t.Errorf("member a: written %d, rolled back %d; want its one row kept", w, rb)
	}
	if w, c, rb := b.counts(); w != 0 || c != 0 || rb != 0 {
		t.Errorf("member b had nothing in this batch and was still touched: written %d, committed %d, rolled back %d", w, c, rb)
	}
}

func TestOneMemberRefusingItsRowRollsBackTheOthers(t *testing.T) {
	a := &txSink{name: "a"}
	b := &txSink{name: "b", prepareErr: errors.New("prepare refused")}
	g := newGroup(t, Member{ID: "node-a", Sink: a}, Member{ID: "node-b", Sink: b})

	err := g.WriteBatch(t.Context(), []hermod.Message{rowFor(t, "r1", "node-a"), rowFor(t, "r2", "node-b")})
	if err == nil {
		t.Fatal("a member that cannot prepare did not fail the batch")
	}
	if _, c, rb := a.counts(); c != 0 || rb != 1 {
		t.Errorf("member a: committed %d, rolled back %d; want its row rolled back with b's", c, rb)
	}
}
