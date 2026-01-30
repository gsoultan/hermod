package transformer

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/evaluator"
)

func init() {
	Register("aggregate", &AggregateTransformer{
		states: make(map[string]*aggState),
	})
}

type aggState struct {
	Count float64   `json:"count"`
	Sum   float64   `json:"sum"`
	Last  time.Time `json:"last"`
	Start time.Time `json:"start"`
}

type AggregateTransformer struct {
	mu     sync.Mutex
	states map[string]*aggState
}

func (t *AggregateTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]interface{}) (hermod.Message, error) {
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

	valRaw := evaluator.GetMsgValByPath(msg, field)
	val, _ := evaluator.ToFloat64(valRaw)

	groupVal := ""
	if groupBy != "" {
		groupVal = fmt.Sprintf("%v", evaluator.GetMsgValByPath(msg, groupBy))
	}

	workflowID, _ := ctx.Value("workflow_id").(string)
	nodeID, _ := ctx.Value("node_id").(string)

	now := time.Now()
	stateKey := ""
	if windowType == "tumbling" && window > 0 {
		windowStart := now.Truncate(window).Unix()
		stateKey = fmt.Sprintf("%s:%s:%s:%s:t:%d", workflowID, nodeID, field, groupVal, windowStart)
	} else {
		stateKey = fmt.Sprintf("%s:%s:%s:%s", workflowID, nodeID, field, groupVal)
	}

	var store hermod.StateStore
	if s, ok := ctx.Value(hermod.StateStoreKey).(hermod.StateStore); ok {
		store = s
	}

	t.mu.Lock()
	state, ok := t.states[stateKey]

	if !ok && persistent && store != nil {
		data, err := store.Get(ctx, "agg:"+stateKey)
		if err == nil && data != nil {
			var s aggState
			if err := json.Unmarshal(data, &s); err == nil {
				state = &s
				t.states[stateKey] = state
				ok = true
			}
		}
	}

	// Reset if session window expired
	if (windowType == "" || windowType == "session") && window > 0 && ok && now.Sub(state.Last) > window {
		ok = false
	}

	if !ok {
		state = &aggState{
			Start: now,
		}
		t.states[stateKey] = state

		// Cleanup old states periodically or if too many
		if len(t.states) > 1000 {
			for k, s := range t.states {
				if now.Sub(s.Last) > window*2 && window > 0 {
					delete(t.states, k)
				}
			}
		}
	}

	state.Count++
	state.Sum += val
	state.Last = now

	currentSum := state.Sum
	currentCount := state.Count

	if persistent && store != nil {
		data, _ := json.Marshal(state)
		_ = store.Set(ctx, "agg:"+stateKey, data)
	}
	t.mu.Unlock()

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
