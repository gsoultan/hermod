package transformer

import (
	"context"
	"fmt"
	"math"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/evaluator"
)

func init() {
	Register("dq_scorer", &DQScorerTransformer{})
}

// DQScorerTransformer calculates a data quality score based on schema adherence and field completeness.
type DQScorerTransformer struct{}

func (t *DQScorerTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]interface{}) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}

	requiredFields, _ := config["required_fields"].([]interface{})
	data := msg.Data()
	if data == nil {
		return msg, nil
	}

	score := 100.0
	missingCount := 0
	totalRequired := len(requiredFields)

	if totalRequired > 0 {
		for _, f := range requiredFields {
			fieldPath, _ := f.(string)
			val := evaluator.GetMsgValByPath(msg, fieldPath)
			if val == nil {
				missingCount++
			}
		}

		penalty := (float64(missingCount) / float64(totalRequired)) * 50.0
		score -= penalty
	}

	// Anomaly/Drift check (integration with StatValidator)
	if msg.Metadata()["anomaly"] == "true" {
		score -= 20.0
	}

	score = math.Max(0, math.Min(100, score))
	msg.SetMetadata("dq_score", fmt.Sprintf("%.1f", score))
	msg.SetData("_dq_score", score)

	threshold, _ := config["min_score"].(float64)
	if threshold > 0 && score < threshold {
		return nil, fmt.Errorf("dq_scorer: quality score %.1f below threshold %.1f", score, threshold)
	}

	return msg, nil
}
