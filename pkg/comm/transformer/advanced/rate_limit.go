// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package advanced

import (
	"context"
	"fmt"
	"sync"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/transformer"

	"github.com/gsoultan/hermod/pkg/infra/evaluator"
	"golang.org/x/time/rate"
)

func init() {
	transformer.Register("rate_limit", &RateLimitTransformer{})
}

type RateLimitTransformer struct {
	limiters sync.Map // map[string]*rate.Limiter
}

func (t *RateLimitTransformer) Prepare(config map[string]any) (map[string]any, error) {
	mps, _ := evaluator.ToFloat64(config["mps"])
	if mps <= 0 {
		mps = 100
	}
	config["_parsed_mps"] = mps

	burst, _ := evaluator.ToFloat64(config["burst"])
	if burst <= 0 {
		burst = mps
	}
	config["_parsed_burst"] = burst

	return config, nil
}

func (t *RateLimitTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}

	var mps float64
	if v, ok := config["_parsed_mps"].(float64); ok {
		mps = v
	} else {
		mps, _ = evaluator.ToFloat64(config["mps"])
		if mps <= 0 {
			mps = 100
		}
	}

	var burst float64
	if v, ok := config["_parsed_burst"].(float64); ok {
		burst = v
	} else {
		burst, _ = evaluator.ToFloat64(config["burst"])
		if burst <= 0 {
			burst = mps
		}
	}

	key := "default"
	// Optional: rate limit per specific field value (e.g. per user_id)
	field, _ := config["keyField"].(string)
	if field != "" {
		if val := evaluator.EvaluateField(msg, field); val != nil {
			key = fmt.Sprintf("%v", val)
		}
	}

	actual, _ := t.limiters.LoadOrStore(key, rate.NewLimiter(rate.Limit(mps), int(burst)))
	limiter := actual.(*rate.Limiter)

	// Update limit if it changed in config (limiters are shared between messages)
	if limiter.Limit() != rate.Limit(mps) {
		limiter.SetLimit(rate.Limit(mps))
	}
	if limiter.Burst() != int(burst) {
		limiter.SetBurst(int(burst))
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
