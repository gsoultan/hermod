package control

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/engine/registry/interfaces"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
)

func init() {
	interfaces.RegisterNodeExecutor("explode", &ExplodeNode{})
}

// ExplodeNode turns one record with an array into one record per element,
// keeping the record's other fields on each.
//
// It is a node rather than a transformer because a transformer returns one
// message; emitting several is what node executors do, as foreach does. It
// differs from foreach in where the element goes: foreach adds `_item` and
// `_index` beside the array, explode puts the element where the record's
// shape wants it and drops the array.
//
// Config:
//   - arrayPath: the array to explode. Required.
//   - mode: "field" (default) writes the element to targetField, default the
//     array's own path; "merge" writes an object element's fields onto the
//     record, the element's value winning over a field of the same name.
//   - indexField: if set, receives the element's position.
//   - maxItems: the most elements one record may become, default 10000. More
//     fails the record rather than emitting part of it.
//   - keepEmpty: an empty array passes the record on unchanged rather than
//     emitting nothing.
//
// Each output carries the `_fanout_*` metadata a collect node regroups by.
type ExplodeNode struct{}

func (n *ExplodeNode) Execute(_ context.Context, _ interfaces.NodeContext, _ string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
	arrayPath, _ := node.Config["arrayPath"].(string)
	arrayPath = strings.TrimSpace(arrayPath)
	if arrayPath == "" {
		return nil, "", errors.New("explode: arrayPath is required")
	}
	arr, ok := evaluator.GetMsgRawValByPath(msg, arrayPath).([]any)
	if !ok {
		return nil, "", fmt.Errorf("explode: value at %s is not an array", arrayPath)
	}
	if len(arr) == 0 {
		if evaluator.ToBool(node.Config["keepEmpty"]) {
			return []hermod.Message{msg}, "", nil
		}
		return nil, "", nil
	}
	if maxItems := configuredMaxItems(node.Config); len(arr) > maxItems {
		return nil, "", fmt.Errorf("explode: %s holds %d elements, above the %d this node emits; "+
			"raise the node's maxItems setting to allow more", arrayPath, len(arr), maxItems)
	}
	mode, _ := node.Config["mode"].(string)
	merge := strings.EqualFold(strings.TrimSpace(mode), "merge")
	if merge {
		for i, item := range arr {
			if _, ok := item.(map[string]any); !ok {
				return nil, "", fmt.Errorf("explode: element %d of %s is %T, and only an object can be merged", i, arrayPath, item)
			}
		}
	}
	return explode(msg, node.Config, arrayPath, merge), "", nil
}

// explode emits the messages. Like foreach it clones once and removes the
// array from that base before cloning per element, so each output costs its
// own element and not the whole array again.
func explode(msg hermod.Message, config map[string]any, arrayPath string, merge bool) []hermod.Message {
	base := msg.Clone()
	defer base.Release()
	// The base's own copy of the array: each element goes to exactly one
	// output and shares nothing with the caller's message.
	items, _ := evaluator.GetMsgRawValByPath(base, arrayPath).([]any)
	deleteByPath(base.DataRef(), arrayPath)

	target, _ := config["targetField"].(string)
	if target = strings.TrimSpace(target); target == "" {
		target = arrayPath
	}
	indexField, _ := config["indexField"].(string)
	total := strconv.Itoa(len(items))
	out := make([]hermod.Message, 0, len(items))
	for i, item := range items {
		m := base.Clone()
		if obj, ok := item.(map[string]any); merge && ok {
			for _, k := range slices.Sorted(maps.Keys(obj)) {
				m.SetData(k, obj[k])
			}
		} else {
			m.SetData(target, item)
		}
		if indexField != "" {
			m.SetData(indexField, i)
		}
		m.SetMetadata("_fanout_group", msg.ID())
		m.SetMetadata("_fanout_index", strconv.Itoa(i))
		m.SetMetadata("_fanout_total", total)
		out = append(out, m)
	}
	return out
}
