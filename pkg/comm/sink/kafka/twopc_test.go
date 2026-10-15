// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package kafka

import (
	"testing"

	"github.com/gsoultan/hermod"
)

// TestKafkaSinkDoesNotClaimTwoPhaseCommit is a guard, not a feature test.
//
// KafkaSink used to satisfy hermod.TwoPhaseCommit with six methods that all
// returned nil. That is worse than not implementing it: a coordinator calling
// RollbackPrepared would receive a nil error and conclude the rollback
// succeeded, while the records stayed committed in Kafka. A silent divergence
// between what the coordinator believes and what the broker holds is the worst
// failure mode a distributed transaction has.
//
// The sink now runs real Kafka transactions (NewTransactionalKafkaSink), and
// still must not satisfy the interface. A Kafka transaction has no prepared
// state that outlives its producer: the next producer with the same
// transactional ID aborts whatever was left open. A coordinator that prepared
// the sink, committed its other participants and then lost the process would
// find this participant rolled back. Each batch is its own transaction
// instead, committed or aborted before WriteBatch returns.
//
// So type assertions elsewhere must stay false, and a transaction group must
// not accept a Kafka sink as a member.
func TestKafkaSinkDoesNotClaimTwoPhaseCommit(t *testing.T) {
	var s any = &KafkaSink{}

	if _, ok := s.(hermod.TwoPhaseCommit); ok {
		t.Fatal("KafkaSink satisfies hermod.TwoPhaseCommit. " +
			"If the methods are no-ops, a coordinator will treat a failed rollback as a successful one. " +
			"Either implement real transactional-producer semantics or remove the methods.")
	}
}

// TestKafkaSinkDoesNotClaimTransactional covers the embedded half of the
// interface for the same reason: Begin/Commit/Rollback returning nil advertises
// atomicity the sink cannot provide.
func TestKafkaSinkDoesNotClaimTransactional(t *testing.T) {
	var s any = &KafkaSink{}

	if _, ok := s.(hermod.Transactional); ok {
		t.Fatal("KafkaSink satisfies hermod.Transactional with no-op methods; " +
			"remove them or back them with a transactional producer")
	}
}

// TestKafkaSinkIsStillASink makes sure the removal above did not take the
// actual contract with it.
func TestKafkaSinkIsStillASink(t *testing.T) {
	var s any = &KafkaSink{}

	if _, ok := s.(hermod.Sink); !ok {
		t.Fatal("KafkaSink no longer satisfies hermod.Sink")
	}
}
