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
	"strings"
	"testing"

	"github.com/gsoultan/hermod"
)

// "Every database must get a row".
//
// By default a member that got no row for an event is left out of that event's
// transaction, and the others commit: a filter on one branch dropping its row is
// a decision, not a failure. A transaction can say otherwise. With the rule on,
// an event that reached some members and not others is refused whole before
// anything is written, and the engine parks it in the dead-letter sink.

func TestWithoutTheRuleAnEventMissingAMembersRowIsAccepted(t *testing.T) {
	g := newGroup(t, Member{ID: "node-a", Sink: &txSink{name: "a"}}, Member{ID: "node-b", Sink: &txSink{name: "b"}})
	if err := g.ValidateBundle(t.Context(), []hermod.Message{rowFor(t, "r1", "node-a")}); err != nil {
		t.Errorf("ValidateBundle refused an event with the rule off: %v", err)
	}
}

func TestWithTheRuleAnEventMissingAMembersRowIsRefusedNamingTheMember(t *testing.T) {
	g := newGroup(t,
		Member{ID: "node-a", Name: "Orders", Sink: &txSink{name: "a"}},
		Member{ID: "node-b", Name: "Invoices", Sink: &txSink{name: "b"}},
		Member{ID: "node-c", Name: "Ledger", Sink: &txSink{name: "c"}},
	)
	g.SetRequireEveryMember(true)

	err := g.ValidateBundle(t.Context(), []hermod.Message{rowFor(t, "r1", "node-a"), rowFor(t, "r2", "node-c")})
	if err == nil {
		t.Fatal("an event with no row for Invoices was accepted")
	}
	if !errors.Is(err, hermod.ErrPermanent) {
		t.Errorf("the refusal %v is not permanent: retrying the same event cannot add the row", err)
	}
	if !strings.Contains(err.Error(), "Invoices") || strings.Contains(err.Error(), "Orders") {
		t.Errorf("the refusal %q should name the member with no row, and only it", err)
	}
}

func TestWithTheRuleAnEventWithARowForEveryMemberIsAccepted(t *testing.T) {
	g := newGroup(t, Member{ID: "node-a", Sink: &txSink{name: "a"}}, Member{ID: "node-b", Sink: &txSink{name: "b"}})
	g.SetRequireEveryMember(true)
	if err := g.ValidateBundle(t.Context(), []hermod.Message{rowFor(t, "r1", "node-b"), rowFor(t, "r2", "node-a")}); err != nil {
		t.Errorf("ValidateBundle: %v", err)
	}
}

func TestWithTheRuleARowThatNamesNoMemberCountsForEveryMember(t *testing.T) {
	g := newGroup(t, Member{ID: "a", Sink: &txSink{name: "a"}}, Member{ID: "b", Sink: &txSink{name: "b"}})
	g.SetRequireEveryMember(true)
	if err := g.ValidateBundle(t.Context(), []hermod.Message{rowFor(t, "r1", "")}); err != nil {
		t.Errorf("a row for every member was refused: %v", err)
	}
}

func TestARefusedEventIsCountedAsARefusedRun(t *testing.T) {
	g := newGroup(t,
		Member{ID: "node-a", Name: "Orders", Sink: &txSink{name: "a"}},
		Member{ID: "node-b", Name: "Invoices", Sink: &txSink{name: "b"}},
	)
	g.SetRequireEveryMember(true)
	_ = g.ValidateBundle(t.Context(), []hermod.Message{rowFor(t, "r1", "node-a")})

	st := g.Stats()
	if st.Refused != 1 {
		t.Errorf("Refused = %d, want 1", st.Refused)
	}
	if len(st.Recent) != 1 || st.Recent[0].Outcome != OutcomeRefused {
		t.Fatalf("Recent = %+v, want one refused run", st.Recent)
	}
	got := map[string]RunMember{}
	for _, m := range st.Recent[0].Members {
		got[m.ID] = m
	}
	if got["node-a"].Rows != 1 || got["node-a"].Missing {
		t.Errorf("Orders in the run = %+v, want its one row", got["node-a"])
	}
	if !got["node-b"].Missing {
		t.Errorf("Invoices in the run = %+v, want it marked as the member with no row", got["node-b"])
	}
	if m := memberStats(st, "node-b"); m.Missing != 1 {
		t.Errorf("Invoices stats = %+v, want Missing 1", m)
	}
}

func memberStats(st Stats, id string) MemberStats {
	for _, m := range st.Members {
		if m.ID == id {
			return m
		}
	}
	return MemberStats{}
}
