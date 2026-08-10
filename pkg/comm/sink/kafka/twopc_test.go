package kafka

import (
	"testing"

	"github.com/user/hermod"
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
// Real Kafka EOS needs a transactional producer (InitTransactions /
// BeginTransaction / AbortTransaction). segmentio/kafka-go, the client used
// here, does not expose one — implementing this requires moving to franz-go or
// confluent-kafka-go first.
//
// So: until that work happens, KafkaSink must NOT satisfy the interface. Type
// assertions elsewhere are then correctly false and callers fall back to
// at-least-once delivery, which is what actually happens.
//
// If you are here because you just implemented real Kafka transactions: delete
// this test in the same commit that makes it fail.
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
