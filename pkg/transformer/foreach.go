package transformer

import (
	"context"
	"errors"
	"strconv"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/evaluator"
)

// ForeachTransformer iterates over an array field in the message and
// materializes a fanout array under a target field on the same message.
// It does not emit multiple messages at runtime; instead it prepares
// the expanded array for downstream consumers or preview UI.
//
// Config:
// - arrayPath (string, required): dot-path to an array in the message data
// - resultField (string, optional): where to store the resulting array; default: "_fanout"
// - itemPath (string, optional): if set and items are objects, extracts a nested value per item
// - indexField (string, optional): if set and items are objects, writes the item index to this field
// - limit (string/int, optional): max items to include (>0)
// - dropEmpty (bool, optional): if true and no items, the message is dropped (filtered)
func init() {
	Register("foreach", &ForeachTransformer{})
	Register("fanout", &ForeachTransformer{})
}

type ForeachTransformer struct{}

func (t *ForeachTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}

	arrayPath, _ := config["arrayPath"].(string)
	if arrayPath == "" {
		return msg, errors.New("foreach: arrayPath is required")
	}

	resultField, _ := config["resultField"].(string)
	if resultField == "" {
		resultField = "_fanout"
	}

	itemPath, _ := config["itemPath"].(string)
	indexField, _ := config["indexField"].(string)

	// Parse limit which might come as string from UI/storage
	var limit int
	switch v := config["limit"].(type) {
	case float64:
		limit = int(v)
	case int:
		limit = v
	case string:
		if v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				limit = n
			}
		}
	}

	dropEmpty := evaluator.ToBool(config["dropEmpty"]) || evaluator.ToBool(config["drop_empty"]) // allow alt key

	raw := evaluator.GetMsgValByPath(msg, arrayPath)

	// Normalize to []any
	var arr []any
	if raw == nil {
		arr = nil
	} else if v, ok := raw.([]any); ok {
		arr = v
	} else if vMapSlice, ok := raw.([]map[string]any); ok {
		arr = make([]any, 0, len(vMapSlice))
		for _, m := range vMapSlice {
			arr = append(arr, m)
		}
	} else {
		// Not an array; return error to let onError/statusField logic handle it
		return msg, errors.New("foreach: value at arrayPath is not an array")
	}

	if len(arr) == 0 {
		if dropEmpty {
			return nil, nil
		}
		// Ensure an empty array is materialized
		msg.SetData(resultField, []any{})
		return msg, nil
	}

	// Build results
	max := len(arr)
	if limit > 0 && limit < max {
		max = limit
	}

	out := make([]any, 0, max)
	for i := 0; i < max; i++ {
		it := arr[i]

		// If itemPath is set and item is object, extract nested value
		if itemPath != "" {
			if m, ok := it.(map[string]any); ok {
				it = evaluator.GetValByPath(m, itemPath)
			}
		}

		// If indexField is set and item is object, set index on a shallow copy
		if indexField != "" {
			if m, ok := it.(map[string]any); ok {
				cp := make(map[string]any, len(m)+1)
				for k, v := range m {
					cp[k] = v
				}
				cp[indexField] = i
				it = cp
			}
		}

		out = append(out, it)
	}

	msg.SetData(resultField, out)
	return msg, nil
}
