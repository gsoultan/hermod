package advanced

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/user/hermod/pkg/comm/transformer"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/infra/evaluator"
)

func init() {
	transformer.Register("aggregate", &AggregateTransformer{
		states: make(map[string]*aggState),
	})
}

// aggState represents the current state of an aggregation window.
type aggState struct {
	Count     float64   `json:"count"`
	Sum       float64   `json:"sum"`
	Last      time.Time `json:"last"`
	Start     time.Time `json:"start"`
	IsSession bool      `json:"is_session"`
}

type AggregateTransformer struct {
	mu     sync.Mutex
	states map[string]*aggState
}

func (t *AggregateTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}

	field, _ := config["field"].(string)
	groupBy, _ := config["groupBy"].(string)
	aggType, _ := config["type"].(string) // "sum", "count", "avg"
	windowStr, _ := config["window"].(string)
	windowType, _ := config["windowType"].(string) // "tumbling", "sliding", "session" (default)
	persistent := evaluator.ToBool(config["persistent"])

	window := 0 * time.Second
	if windowStr != "" {
		window, _ = time.ParseDuration(windowStr)
	}

	valRaw := evaluator.EvaluateField(msg, field)
	val, _ := evaluator.ToFloat64(valRaw)

	groupVal := ""
	if groupBy != "" {
		groupVal = fmt.Sprintf("%v", evaluator.EvaluateField(msg, groupBy))
	}

	workflowID, _ := ctx.Value(hermod.WorkflowIDKey).(string)
	nodeID, _ := ctx.Value(hermod.NodeIDKey).(string)

	now := time.Now()
	stateKey := ""
	var stateKeys []string

	if windowType == "tumbling" && window > 0 {
		windowStart := now.Truncate(window).Unix()
		stateKey = fmt.Sprintf("%s:%s:%s:%s:t:%d", workflowID, nodeID, field, groupVal, windowStart)
		stateKeys = append(stateKeys, stateKey)
	} else if windowType == "sliding" && window > 0 {
		slideStr, _ := config["slide"].(string)
		slide := window / 2
		if slideStr != "" {
			if s, err := time.ParseDuration(slideStr); err == nil && s > 0 {
				slide = s
			}
		}
		// A message belongs to multiple sliding windows.
		// We update all windows that cover 'now'.
		// Window i starts at i*slide and ends at i*slide + window.
		// So i*slide <= now < i*slide + window
		// i <= now/slide and i > (now-window)/slide
		firstIndex := now.Add(-window).UnixNano()/slide.Nanoseconds() + 1
		lastIndex := now.UnixNano() / slide.Nanoseconds()

		for i := firstIndex; i <= lastIndex; i++ {
			sKey := fmt.Sprintf("%s:%s:%s:%s:s:%d", workflowID, nodeID, field, groupVal, i)
			stateKeys = append(stateKeys, sKey)
		}
		// For the current message's return value, we'll use the most recent window
		stateKey = stateKeys[len(stateKeys)-1]
	} else {
		stateKey = fmt.Sprintf("%s:%s:%s:%s", workflowID, nodeID, field, groupVal)
		stateKeys = append(stateKeys, stateKey)
	}

	var store hermod.StateStore
	if s, ok := ctx.Value(hermod.StateStoreKey).(hermod.StateStore); ok {
		store = s
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	var currentState *aggState
	for _, sKey := range stateKeys {
		state, ok := t.states[sKey]

		if !ok && persistent && store != nil {
			data, err := store.Get(ctx, "agg:"+sKey)
			if err == nil && data != nil {
				var s aggState
				if err := json.Unmarshal(data, &s); err == nil {
					state = &s
					t.states[sKey] = state
					ok = true
				}
			}
		}

		// Reset if session window expired
		if (windowType == "" || windowType == "session") && window > 0 && ok && time.Since(state.Last) > window {
			ok = false
		}

		if !ok {
			state = &aggState{
				Start: now,
			}
			t.states[sKey] = state
		}

		state.Count++
		state.Sum += val
		state.Last = now

		if sKey == stateKey {
			currentState = state
		}

		if persistent && store != nil {
			data, _ := json.Marshal(state)
			_ = store.Set(ctx, "agg:"+sKey, data)
		}
	}

	// Cleanup old states periodically
	if len(t.states) > 1000 {
		for k, s := range t.states {
			if time.Since(s.Last) > window*3 && window > 0 {
				delete(t.states, k)
			}
		}
	}

	currentSum := currentState.Sum
	currentCount := currentState.Count

	targetField, _ := config["targetField"].(string)
	if targetField == "" {
		targetField = field + "_" + aggType
	}

	switch aggType {
	case "sum":
		msg.SetData(targetField, currentSum)
	case "count":
		msg.SetData(targetField, currentCount)
	case "avg":
		if currentCount > 0 {
			msg.SetData(targetField, currentSum/currentCount)
		} else {
			msg.SetData(targetField, 0)
		}
	}

	return msg, nil
}
