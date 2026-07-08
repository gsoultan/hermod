package advanced

import (
	"context"
	"os"
	"testing"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/comm/message"
	"github.com/user/hermod/pkg/infra/state"
)

func TestAggregateTransformer_Persistence(t *testing.T) {
	dbPath := "test_aggregate_state.db"
	os.Remove(dbPath)
	defer os.Remove(dbPath)

	ss, err := state.NewSQLiteStateStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create state store: %v", err)
	}

	tr := &AggregateTransformer{}

	// Create context with state store and IDs
	ctx := context.WithValue(t.Context(), hermod.StateStoreKey, ss)
	ctx = context.WithValue(ctx, hermod.WorkflowIDKey, "wf1")
	ctx = context.WithValue(ctx, hermod.NodeIDKey, "node1")

	config := map[string]any{
		"field":      "price",
		"type":       "sum",
		"persistent": true,
	}

	// First message
	msg1 := message.AcquireMessage()
	msg1.SetData("price", 10.0)
	_, _ = tr.Transform(ctx, msg1, config)
	if msg1.Data()["price_sum"].(float64) != 10.0 {
		t.Errorf("expected 10.0, got %v", msg1.Data()["price_sum"])
	}

	// Second message
	msg2 := message.AcquireMessage()
	msg2.SetData("price", 20.0)
	_, _ = tr.Transform(ctx, msg2, config)
	if msg2.Data()["price_sum"].(float64) != 30.0 {
		t.Errorf("expected 30.0, got %v", msg2.Data()["price_sum"])
	}

	// Create a NEW transformer instance (simulating restart)
	tr2 := &AggregateTransformer{}

	// Third message - should load state from store
	msg3 := message.AcquireMessage()
	msg3.SetData("price", 5.0)
	_, err = tr2.Transform(ctx, msg3, config)
	if err != nil {
		t.Errorf("transform error: %v", err)
	}

	val, ok := msg3.Data()["price_sum"].(float64)
	if !ok || val != 35.0 {
		t.Errorf("expected 35.0 (loaded from persistence), got %v", msg3.Data()["price_sum"])
	}
}
