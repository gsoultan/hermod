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
	transformer.Register("aggregate", &AggregateTransformer{})
}

// aggState represents the current state of an aggregation window.
type aggState struct {
	mu        sync.Mutex `json:"-"`
	Count     float64    `json:"count"`
	Sum       float64    `json:"sum"`
	Min       float64    `json:"min"`
	Max       float64    `json:"max"`
	Last      time.Time  `json:"last"`
	Start     time.Time  `json:"start"`
	IsSession bool       `json:"is_session"`
}

type AggregateTransformer struct {
	states sync.Map // map[string]*aggState
}

func (t *AggregateTransformer) Prepare(config map[string]any) (map[string]any, error) {
	windowStr, _ := config["window"].(string)
	if windowStr != "" {
		if d, err := time.ParseDuration(windowStr); err == nil {
			config["_parsed_window"] = d
		}
	}
	slideStr, _ := config["slide"].(string)
	if slideStr != "" {
		if d, err := time.ParseDuration(slideStr); err == nil {
			config["_parsed_slide"] = d
		}
	}
	return config, nil
}

func (t *AggregateTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}

	field, _ := config["field"].(string)
	groupBy, _ := config["groupBy"].(string)
	aggType, _ := config["type"].(string)          // "sum", "count", "avg"
	windowType, _ := config["windowType"].(string) // "tumbling", "sliding", "session" (default)
	persistent := evaluator.ToBool(config["persistent"])

	var window time.Duration
	if d, ok := config["_parsed_window"].(time.Duration); ok {
		window = d
	} else {
		windowStr, _ := config["window"].(string)
		if windowStr != "" {
			window, _ = time.ParseDuration(windowStr)
		}
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
		var slide time.Duration
		if d, ok := config["_parsed_slide"].(time.Duration); ok {
			slide = d
		} else {
			slideStr, _ := config["slide"].(string)
			slide = window / 2
			if slideStr != "" {
				if s, err := time.ParseDuration(slideStr); err == nil && s > 0 {
					slide = s
				}
			}
		}
		// A message belongs to multiple sliding windows.
		// We update all windows that cover 'now'.
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

	var currentState *aggState
	for _, sKey := range stateKeys {
		actual, _ := t.states.LoadOrStore(sKey, &aggState{Start: now})
		state := actual.(*aggState)

		state.mu.Lock()

		// If persistent and newly created (Count == 0), try to load from store
		if state.Count == 0 && persistent && store != nil {
			data, err := store.Get(ctx, "agg:"+sKey)
			if err == nil && data != nil {
				_ = json.Unmarshal(data, state)
			}
		}

		if (windowType == "" || windowType == "session") && window > 0 && state.Count > 0 && time.Since(state.Last) > window {
			state.Count = 0
			state.Sum = 0
			state.Min = 0
			state.Max = 0
			state.Start = now
		}

		if state.Count == 0 {
			state.Min = val
			state.Max = val
		} else {
			if val < state.Min {
				state.Min = val
			}
			if val > state.Max {
				state.Max = val
			}
		}

		state.Count++
		state.Sum += val
		state.Last = now

		if sKey == stateKey {
			currentState = &aggState{
				Count: state.Count,
				Sum:   state.Sum,
				Min:   state.Min,
				Max:   state.Max,
			}
		}

		if persistent && store != nil {
			data, _ := json.Marshal(state)
			// Unlock BEFORE I/O if possible, but we need to keep state consistent.
			// Actually, we can copy the data and unlock.
			state.mu.Unlock()
			_ = store.Set(ctx, "agg:"+sKey, data)
		} else {
			state.mu.Unlock()
		}
	}

	currentSum := currentState.Sum
	currentCount := currentState.Count
	currentMin := currentState.Min
	currentMax := currentState.Max

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
	case "min":
		msg.SetData(targetField, currentMin)
	case "max":
		msg.SetData(targetField, currentMax)
	}

	return msg, nil
}
