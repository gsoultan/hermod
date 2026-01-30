package transformer

import (
	"context"

	"github.com/user/hermod"
	"golang.org/x/time/rate"
)

func init() {
	Register("rate_limit", &RateLimitTransformer{
		limiters: make(map[string]*rate.Limiter),
	})
}

type RateLimitTransformer struct {
	limiters map[string]*rate.Limiter
}

func (t *RateLimitTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]interface{}) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}

	mps, _ := config["mps"].(float64)
	if mps <= 0 {
		mps = 100 // Default 100 msg/sec
	}

	burst, _ := config["burst"].(float64)
	if burst <= 0 {
		burst = mps // Default burst = mps
	}

	key := "default"
	// Optional: rate limit per specific field value (e.g. per user_id)
	field, _ := config["keyField"].(string)
	if field != "" {
		if val := msg.Data()[field]; val != nil {
			key = interfaceToString(val)
		}
	}

	limiter, ok := t.limiters[key]
	if !ok {
		limiter = rate.NewLimiter(rate.Limit(mps), int(burst))
		t.limiters[key] = limiter
	} else {
		// Update limit if it changed in config
		if limiter.Limit() != rate.Limit(mps) {
			limiter.SetLimit(rate.Limit(mps))
		}
		if limiter.Burst() != int(burst) {
			limiter.SetBurst(int(burst))
		}
	}

	strategy, _ := config["strategy"].(string) // "wait" or "drop"
	if strategy == "drop" {
		if !limiter.Allow() {
			return nil, nil // Drop message
		}
	} else {
		// Wait
		if err := limiter.Wait(ctx); err != nil {
			return nil, err
		}
	}

	return msg, nil
}

func interfaceToString(v interface{}) string {
	switch val := v.(type) {
	case string:
		return val
	default:
		return ""
	}
}
