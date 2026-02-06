package transformer

import (
	"context"
	"math/rand"
	"strconv"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/evaluator"
)

func init() {
	Register("sampling", &SamplingTransformer{
		rng: rand.New(rand.NewSource(time.Now().UnixNano())),
	})
}

type SamplingTransformer struct {
	rng *rand.Rand
}

func (t *SamplingTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]interface{}) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}

	sampleType, _ := config["type"].(string) // "percentage" or "row"

	if sampleType == "percentage" || sampleType == "" {
		percentage, _ := evaluator.ToFloat64(config["percentage"])
		if percentage <= 0 {
			return nil, nil // Filter all
		}
		if percentage >= 100 {
			return msg, nil // Keep all
		}

		if t.rng.Float64()*100 < percentage {
			return msg, nil
		}
		return nil, nil
	} else if sampleType == "row" {
		// Row sampling (every Nth row)
		// This needs state to keep track of row count
		// For now, let's stick to percentage as it's more common in stateless transformations
		// or use a random probability for row-like behavior if N is known.
		// Actually, row sampling in SSIS often means "take first N rows" or "take random N rows".
		// Random N rows is hard without knowing total count.
		// Let's implement "every Nth row" using state store.

		n, _ := evaluator.ToInt64(config["n"])
		if n <= 1 {
			return msg, nil
		}

		workflowID, _ := ctx.Value("workflow_id").(string)
		nodeID, _ := ctx.Value("node_id").(string)
		stateKey := "sampling:" + workflowID + ":" + nodeID

		var store hermod.StateStore
		if s, ok := ctx.Value(hermod.StateStoreKey).(hermod.StateStore); ok {
			store = s
		}

		if store != nil {
			count := int64(0)
			data, err := store.Get(ctx, stateKey)
			if err == nil && data != nil {
				count, _ = strconv.ParseInt(string(data), 10, 64)
			}
			count++
			_ = store.Set(ctx, stateKey, []byte(strconv.FormatInt(count, 10)))

			if count%n == 0 {
				return msg, nil
			}
			return nil, nil
		}
	}

	return msg, nil
}
