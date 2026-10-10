package structure

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/transformer"
	"github.com/gsoultan/hermod/pkg/comm/transformer/core"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
)

func init() {
	transformer.Register("field_diff", &FieldDiff{})
}

const defaultDiffTarget = "changes"

// FieldDiff compares a change event's before- and after-image and reports the
// columns that differ, each as {"old": …, "new": …}.
//
// The images are read the way the engine carries them: the before-image from
// the message's envelope (what a CDC source sets) and the after-image from its
// data. A record that holds both as "before" and "after" fields — a
// Debezium-style body posted to a webhook — is read from those instead. An
// insert has no before-image, so every column is new; a delete has no
// after-image, so every column goes to null.
//
// Values are compared by their JSON form, so 2 and 2.0, or the same object in
// another key order, are not changes.
//
// Config:
//   - targetField: where the changes go, default "changes".
//   - ignoreColumns: columns never reported (a list or comma-separated),
//     e.g. updated_at.
//   - onlyChanges: the record's data becomes the changes alone; the envelope
//     (operation, table, before-image) is kept.
//   - dropUnchanged: drop a record with no changes.
type FieldDiff struct{}

func (d *FieldDiff) Transform(_ context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}
	before, after, err := diffImages(msg)
	if err != nil {
		return msg, fmt.Errorf("field_diff: %w", err)
	}
	target := configString(config, "targetField", defaultDiffTarget)
	ignore := ignoredColumns(config)
	ignore[target] = true

	changes := map[string]any{}
	for _, col := range slices.Sorted(maps.Keys(unionKeys(before, after))) {
		if ignore[col] {
			continue
		}
		oldV, newV := before[col], after[col]
		if !sameJSON(oldV, newV) {
			changes[col] = map[string]any{"old": oldV, "new": newV}
		}
	}

	if len(changes) == 0 && evaluator.ToBool(config["dropUnchanged"]) {
		return nil, nil
	}
	if evaluator.ToBool(config["onlyChanges"]) {
		replaceData(msg, changes)
		return msg, nil
	}
	msg.SetData(target, changes)
	return msg, nil
}

// diffImages returns the before- and after-image as maps.
func diffImages(msg hermod.Message) (before, after map[string]any, err error) {
	data := msg.Data()
	if raw := msg.Before(); len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &before); err != nil {
			return nil, nil, fmt.Errorf("the before-image is not a JSON object: %w", err)
		}
	} else if b, ok := data["before"].(map[string]any); ok {
		before = b
	}
	if a, ok := data["after"].(map[string]any); ok {
		return before, a, nil
	}
	// The data is the after-image; a "before" field in it is the other image,
	// not a column. data is Data()'s copy, so deleting from it is safe.
	if _, ok := data["before"].(map[string]any); ok {
		delete(data, "before")
	}
	return before, data, nil
}

func ignoredColumns(config map[string]any) map[string]bool {
	cols := core.GetConfigStringSlice(config, "ignoreColumns")
	if len(cols) == 0 {
		cols = core.SplitComma(core.GetConfigString(config, "ignoreColumns"))
	}
	out := make(map[string]bool, len(cols)+1)
	for _, c := range cols {
		out[c] = true
	}
	return out
}

func unionKeys(a, b map[string]any) map[string]struct{} {
	out := make(map[string]struct{}, len(a)+len(b))
	for k := range a {
		out[k] = struct{}{}
	}
	for k := range b {
		out[k] = struct{}{}
	}
	return out
}

// sameJSON reports whether a and b encode to the same JSON. encoding/json
// sorts map keys, so key order does not count, and an int and the float64 a
// JSON decoder made of it encode alike.
func sameJSON(a, b any) bool {
	ja, errA := json.Marshal(a)
	jb, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return false
	}
	return bytes.Equal(ja, jb)
}
