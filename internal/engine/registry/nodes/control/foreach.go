package control

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/engine/registry/interfaces"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
)

func init() {
	interfaces.RegisterNodeExecutor("foreach", &ForeachNode{})
}

// defaultMaxFanoutItems bounds how many messages one message may become.
//
// The array is upstream-controlled: a single source row decides the fan-out
// width, and therefore how much memory the engine spends on it. The bound is
// generous because the per-item cost is now linear (see Execute); it is here so
// a runaway array fails one message with an actionable error instead of taking
// the worker down, and it can be raised per node with `maxItems`.
const defaultMaxFanoutItems = 10000

// ForeachNode implements execution-level fan-out.
type ForeachNode struct{}

// Execute splits a single message into multiple messages based on an array field.
//
// Each output carries the row it came from, its own item under `_item` and its
// position under `_index`, plus the `_fanout_*` metadata a downstream collect
// node reassembles the group with.
//
// The array itself is *not* carried onto the outputs. hermod.Message.Clone deep
// copies every data value, so cloning once per item copied the whole array onto
// every one of the N clones and the cost of a fan-out grew with the square of
// the array: measured at 3.6 MB for 100 items, 353 MB for 1000 and 5.64 GB for
// 4000 — one order was enough to exhaust a 7 GB runner. Cloning a base that has
// had the array removed makes it linear, and the N-1 items a given message never
// reads are exactly what it was paying for. Set `keepSourceArray` to opt back
// into carrying it, and into the cost.
func (n *ForeachNode) Execute(ctx context.Context, nctx interfaces.NodeContext, workflowID string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
	arrayPath, _ := node.Config["arrayPath"].(string)
	if arrayPath == "" {
		return nil, "", errors.New("foreach: arrayPath is required")
	}

	raw := evaluator.GetMsgValByPath(msg, arrayPath)
	arr, ok := raw.([]any)
	if !ok {
		return nil, "", fmt.Errorf("foreach: value at %s is not an array", arrayPath)
	}

	if len(arr) == 0 {
		return nil, "", nil
	}

	// Checked before anything is cloned, so an oversized array costs nothing.
	// It fails rather than truncating: a truncated fan-out is a partial write to
	// every sink downstream, with no error and nothing to tell it from a short
	// array.
	maxItems := configuredMaxItems(node.Config)
	if len(arr) > maxItems {
		return nil, "", fmt.Errorf(
			"foreach: %s holds %d items, above the %d this node will fan out; "+
				"raise the node's maxItems setting to allow more, or narrow the array upstream",
			arrayPath, len(arr), maxItems)
	}

	// One deep copy. Its array is a private copy of the source's, so handing an
	// element of it to an output message shares nothing with the caller's
	// message or with any other output.
	base := msg.Clone()
	defer base.Release()

	items, _ := evaluator.GetValByPath(base.DataRef(), arrayPath).([]any)
	if len(items) != len(arr) {
		// Should not happen; the clone is a copy of the same map. Falling back to
		// the source's elements keeps the node working rather than emitting
		// messages with no item in them.
		items = arr
	}

	if !evaluator.ToBool(node.Config["keepSourceArray"]) {
		// Removed after `items` has been taken, so the slice stays reachable —
		// this only stops Clone from copying it onto each of the N outputs.
		deleteByPath(base.DataRef(), arrayPath)
	}

	total := strconv.Itoa(len(items))
	results := make([]hermod.Message, 0, len(items))
	for i, item := range items {
		m := base.Clone()
		m.SetData("_item", item)
		m.SetData("_index", i)
		// Correlation/idempotency metadata for downstream sinks and debugging
		m.SetMetadata("_fanout_group", msg.ID())
		m.SetMetadata("_fanout_index", strconv.Itoa(i))
		m.SetMetadata("_fanout_total", total)
		results = append(results, m)
	}

	return results, "", nil
}

// configuredMaxItems reads the per-node cap, which arrives as a string from the
// editor and as a number from an imported bundle. A value of zero or less is
// read as "use the default" rather than as "fan out nothing": a node that
// silently emits no messages is the failure this whole path exists to avoid.
func configuredMaxItems(config map[string]any) int {
	var n int
	switch v := config["maxItems"].(type) {
	case float64:
		n = int(v)
	case int:
		n = v
	case int64:
		n = int(v)
	case string:
		if parsed, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			n = parsed
		}
	}
	if n <= 0 {
		return defaultMaxFanoutItems
	}
	return n
}

// deleteByPath removes a dot-path from a nested map.
//
// It walks to the parent and deletes the leaf there. Deleting the first segment
// instead would take the whole containing object with it, so a foreach over
// `order.lines` would strip `order.id` from every message it emitted.
func deleteByPath(data map[string]any, path string) {
	if data == nil || path == "" {
		return
	}
	segments := strings.Split(path, ".")
	current := data
	for _, seg := range segments[:len(segments)-1] {
		next, ok := current[seg].(map[string]any)
		if !ok {
			return
		}
		current = next
	}
	delete(current, segments[len(segments)-1])
}
