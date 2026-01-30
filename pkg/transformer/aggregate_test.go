package transformer

import (
	"context"
	"testing"
	"time"

	"github.com/user/hermod/pkg/message"
)

func TestAggregateTransformer_Tumbling(t *testing.T) {
	tr := &AggregateTransformer{
		states: make(map[string]*aggState),
	}
	ctx := context.Background()

	config := map[string]interface{}{
		"field":      "price",
		"type":       "sum",
		"window":     "1s",
		"windowType": "tumbling",
	}

	// First message
	msg1 := message.AcquireMessage()
	msg1.SetData("price", 10.0)
	_, _ = tr.Transform(ctx, msg1, config)
	if msg1.Data()["price_sum"].(float64) != 10.0 {
		t.Errorf("expected 10.0, got %v", msg1.Data()["price_sum"])
	}

	// Second message in same window
	msg2 := message.AcquireMessage()
	msg2.SetData("price", 20.0)
	_, _ = tr.Transform(ctx, msg2, config)
	if msg2.Data()["price_sum"].(float64) != 30.0 {
		t.Errorf("expected 30.0, got %v", msg2.Data()["price_sum"])
	}

	// Wait for next window
	time.Sleep(1100 * time.Millisecond)

	// Third message in new window
	msg3 := message.AcquireMessage()
	msg3.SetData("price", 5.0)
	_, _ = tr.Transform(ctx, msg3, config)
	if msg3.Data()["price_sum"].(float64) != 5.0 {
		t.Errorf("expected 5.0, got %v", msg3.Data()["price_sum"])
	}
}
