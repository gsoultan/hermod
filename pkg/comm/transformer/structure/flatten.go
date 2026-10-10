package structure

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/transformer"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
)

func init() {
	transformer.Register("flatten", &Flatten{})
	transformer.Register("unflatten", &Unflatten{})
}

// maxNestingDepth bounds how deep flatten descends and how many segments
// unflatten splits a key into when the node sets no depth of its own. A record
// is upstream data, and recursion it controls is recursion it can exhaust.
const maxNestingDepth = 64

// shapeOptions are the settings flatten and unflatten share, so that the same
// settings on both nodes undo each other.
type shapeOptions struct {
	sep        string
	maxDepth   int
	indexArray bool
}

func readShapeOptions(config map[string]any) shapeOptions {
	// The separator is not trimmed: " " is a legitimate choice.
	sep, _ := config["separator"].(string)
	if sep == "" {
		sep = "_"
	}
	return shapeOptions{
		sep:        sep,
		maxDepth:   min(configInt(config, "maxDepth", maxNestingDepth), maxNestingDepth),
		indexArray: configString(config, "arrays", "index") != "keep",
	}
}

// reshape runs fn over the whole record, or over one object field of it when
// the node names one. A field's result goes to targetField, default the field
// itself; the whole record's result replaces the record.
func reshape(msg hermod.Message, config map[string]any, name string, fn func(map[string]any) (map[string]any, error)) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}
	field := configString(config, "field", "")
	if field == "" {
		out, err := fn(msg.Data())
		if err != nil {
			return msg, fmt.Errorf("%s: %w", name, err)
		}
		replaceData(msg, out)
		return msg, nil
	}
	obj, ok := evaluator.GetMsgValByPath(msg, field).(map[string]any)
	if !ok {
		return msg, fmt.Errorf("%s: field %q is not an object", name, field)
	}
	out, err := fn(obj)
	if err != nil {
		return msg, fmt.Errorf("%s: %w", name, err)
	}
	msg.SetData(configString(config, "targetField", field), out)
	return msg, nil
}

// Flatten turns nested objects into one level of joined keys:
// {"a":{"b":{"c":1}}} becomes {"a_b_c":1}.
//
// Config:
//   - separator: joins the keys, default "_".
//   - maxDepth: how many levels to flatten; deeper objects are kept whole.
//     Default (and ceiling) 64.
//   - arrays: "index" (default) flattens array elements under their position,
//     a_0, a_1; "keep" leaves arrays as they are.
//   - field: flatten only this object field, writing the flat object to
//     targetField (default the field itself). Empty flattens the whole record.
//
// Two paths that join to the same key fail the record rather than one value
// silently replacing the other.
type Flatten struct{}

func (f *Flatten) Transform(_ context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	opts := readShapeOptions(config)
	return reshape(msg, config, "flatten", func(in map[string]any) (map[string]any, error) {
		out := make(map[string]any, len(in))
		for _, k := range slices.Sorted(maps.Keys(in)) {
			if err := opts.place(out, k, in[k], 1); err != nil {
				return nil, err
			}
		}
		return out, nil
	})
}

// place writes v under key, descending into it while it is a non-empty
// container and level is within the depth limit.
func (o shapeOptions) place(out map[string]any, key string, v any, level int) error {
	if level <= o.maxDepth {
		if children := o.children(v); len(children) > 0 {
			for _, k := range slices.Sorted(maps.Keys(children)) {
				if err := o.place(out, key+o.sep+k, children[k], level+1); err != nil {
					return err
				}
			}
			return nil
		}
	}
	if _, taken := out[key]; taken {
		return fmt.Errorf("two fields flatten to the key %q; choose another separator", key)
	}
	out[key] = v
	return nil
}

// children is what flatten descends into: an object's fields, or an array's
// elements keyed by position when arrays are indexed. Anything else, an empty
// container included, has none and is written as it is.
func (o shapeOptions) children(v any) map[string]any {
	switch c := v.(type) {
	case map[string]any:
		return c
	case []any:
		if !o.indexArray {
			return nil
		}
		out := make(map[string]any, len(c))
		for i, item := range c {
			out[strconv.Itoa(i)] = item
		}
		return out
	}
	return nil
}

// Unflatten is Flatten's inverse: {"a_b_c":1} becomes {"a":{"b":{"c":1}}}.
//
// Config is Flatten's. With arrays "index" an object whose keys are exactly
// 0..n-1 becomes an array; with "keep" it stays an object. maxDepth limits how
// many times a key is split; the rest of it stays joined.
//
// A key that is both a value and an object ("a" and "a_b") fails the record.
// Note that "_", the default separator, also splits snake_case names: use the
// separator the record was flattened with.
type Unflatten struct{}

// tree marks the objects unflatten builds, so only those are turned into
// arrays. An object the record already held is left as it was.
type tree map[string]any

func (u *Unflatten) Transform(_ context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	opts := readShapeOptions(config)
	return reshape(msg, config, "unflatten", func(in map[string]any) (map[string]any, error) {
		root := tree{}
		for _, k := range slices.Sorted(maps.Keys(in)) {
			if err := opts.insert(root, k, in[k]); err != nil {
				return nil, err
			}
		}
		out, _ := opts.finish(root).(map[string]any)
		return out, nil
	})
}

func (o shapeOptions) insert(root tree, key string, v any) error {
	parts := strings.Split(key, o.sep)
	if len(parts) > o.maxDepth+1 {
		parts = append(parts[:o.maxDepth], strings.Join(parts[o.maxDepth:], o.sep))
	}
	cur := root
	for _, p := range parts[:len(parts)-1] {
		switch next := cur[p].(type) {
		case nil:
			child := tree{}
			cur[p] = child
			cur = child
		case tree:
			cur = next
		default:
			return fmt.Errorf("key %q needs %q to be an object, but it holds a value", key, p)
		}
	}
	last := parts[len(parts)-1]
	if _, taken := cur[last]; taken {
		return fmt.Errorf("key %q is both a value and an object", key)
	}
	cur[last] = v
	return nil
}

// finish converts the trees back to plain maps, and to arrays where the keys
// are a complete run of indexes and the node asked for that.
func (o shapeOptions) finish(v any) any {
	t, ok := v.(tree)
	if !ok {
		return v
	}
	if o.indexArray {
		if arr, ok := o.asArray(t); ok {
			return arr
		}
	}
	out := make(map[string]any, len(t))
	for k, child := range t {
		out[k] = o.finish(child)
	}
	return out
}

func (o shapeOptions) asArray(t tree) ([]any, bool) {
	arr := make([]any, len(t))
	for k, child := range t {
		i, err := strconv.Atoi(k)
		if err != nil || i < 0 || i >= len(t) || strconv.Itoa(i) != k {
			return nil, false
		}
		arr[i] = o.finish(child)
	}
	return arr, true
}
