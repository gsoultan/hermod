package transformer

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/evaluator"
)

func init() {
	Register("join", &JoinTransformer{})
}

type JoinTransformer struct {
	mu sync.Mutex
}

func (t *JoinTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}

	mode, _ := config["mode"].(string) // "store" or "lookup"
	key, _ := config["key"].(string)
	namespace, _ := config["namespace"].(string)
	fields, _ := config["fields"].([]any) // optional: specific fields to join
	prefix, _ := config["prefix"].(string)

	if namespace == "" {
		namespace = "default"
	}

	joinKey := fmt.Sprintf("%v", evaluator.GetMsgValByPath(msg, key))
	if joinKey == "" || joinKey == "<nil>" {
		return msg, nil
	}

	var store hermod.StateStore
	if s, ok := ctx.Value(hermod.StateStoreKey).(hermod.StateStore); ok {
		store = s
	}

	if store == nil {
		return msg, fmt.Errorf("state store not available for join")
	}

	stateKey := fmt.Sprintf("join:%s:%s", namespace, joinKey)

	if mode == "store" {
		data, err := json.Marshal(msg.Data())
		if err != nil {
			return msg, err
		}
		if err := store.Set(ctx, stateKey, data); err != nil {
			return msg, err
		}
	} else if mode == "lookup" {
		data, err := store.Get(ctx, stateKey)
		if err != nil {
			return msg, nil // Key not found
		}
		if data != nil {
			var joinedData map[string]any
			if err := json.Unmarshal(data, &joinedData); err == nil {
				if prefix == "" {
					prefix = "joined_"
				}

				if len(fields) > 0 {
					for _, f := range fields {
						fName, ok := f.(string)
						if ok {
							if val, ok := joinedData[fName]; ok {
								msg.SetData(prefix+fName, val)
							}
						}
					}
				} else {
					for k, v := range joinedData {
						msg.SetData(prefix+k, v)
					}
				}
			}
		}
	}

	return msg, nil
}
