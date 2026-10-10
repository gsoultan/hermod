// Package features holds the feature-engineering transformers: the steps that
// turn a record into the numbers a model takes, the same way at training and
// at inference.
//
// scale, encode and bucketize are stateless: everything they apply is in the
// node's config, fitted once and pasted in, so a record is transformed the same
// way however many records came before it. rolling and anomaly_score keep a
// window of recent values per key (rolling.go).
//
// A list or an object in config may arrive as JSON, as the editor and an API
// client send it, or as JSON text, as a hand-written workflow or the workflow
// builder may. Every node checks its config in Prepare and again on every
// record, because the engine ignores a Prepare error.
package features

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/transformer"
	"github.com/gsoultan/hermod/pkg/comm/transformer/core"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
)

func init() {
	transformer.Register("scale", &Scale{})
}

// Scale rescales numeric fields with statistics fitted beforehand.
//
// Config:
//   - method: "minmax" ((v-min)/(max-min)) or "zscore" ((v-mean)/std), the
//     default for every row.
//   - fields: rows of {field, targetField, method, min, max, mean, std}.
//     targetField defaults to <field>_scaled.
//   - stats: fitted statistics as {"<field>": {"min","max","mean","std"}}, so
//     they can be pasted from a training run. A row without numbers takes them
//     from here; with no rows, every field named here is scaled.
//   - clip: keep min-max output within [0, 1].
//   - onMissing: "fail" (default) or "skip" a record whose field is missing or
//     not a number.
//
// The statistics are never refitted on the records the node sees: a model is
// served the scale it was trained on.
type Scale struct{}

const (
	methodMinMax = "minmax"
	methodZScore = "zscore"
)

// scaleField is one field's rescaling: (v - offset) / span.
type scaleField struct {
	field, target string
	minmax        bool
	offset, span  float64
}

type scaleSpec struct {
	fields []scaleField
	clip   bool
	skip   bool
}

const scaleCacheKey = "_parsed_scale"

func (s *Scale) Prepare(config map[string]any) (map[string]any, error) {
	spec, err := parseScale(config)
	if err != nil {
		return config, err
	}
	config[scaleCacheKey] = spec
	return config, nil
}

func (s *Scale) Transform(_ context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}
	spec, ok := config[scaleCacheKey].(*scaleSpec)
	if !ok {
		var err error
		if spec, err = parseScale(config); err != nil {
			return msg, err
		}
	}
	// Every field is read before any is written, so a record that fails is
	// left as it came.
	out := make([]float64, len(spec.fields))
	present := make([]bool, len(spec.fields))
	for i, f := range spec.fields {
		v, ok := numberAt(msg, f.field)
		if !ok {
			if spec.skip {
				continue
			}
			return msg, missingErr("scale", f.field, msg)
		}
		scaled := (v - f.offset) / f.span
		if spec.clip && f.minmax {
			scaled = min(max(scaled, 0), 1)
		}
		out[i], present[i] = scaled, true
	}
	for i, f := range spec.fields {
		if present[i] {
			msg.SetData(f.target, out[i])
		}
	}
	return msg, nil
}

// fittedStats is one field's entry in the stats blob, or a row's own numbers.
type fittedStats struct {
	Min, Max, Mean, Std *float64
}

func parseScale(config map[string]any) (*scaleSpec, error) {
	method := core.GetConfigString(config, "method")
	if method == "" {
		method = methodMinMax
	}
	if method != methodMinMax && method != methodZScore {
		return nil, fmt.Errorf("scale: method must be %q or %q, not %q", methodMinMax, methodZScore, method)
	}
	skip, err := onMissingSkip("scale", config)
	if err != nil {
		return nil, err
	}
	stats, err := parseStatsBlob(config["stats"])
	if err != nil {
		return nil, err
	}

	rows, err := objectList(config["fields"])
	if err != nil {
		return nil, fmt.Errorf("scale: fields: %w", err)
	}
	if len(rows) == 0 {
		// Every field the pasted stats name, in a fixed order.
		for _, field := range slices.Sorted(maps.Keys(stats)) {
			rows = append(rows, map[string]any{"field": field})
		}
	}
	if len(rows) == 0 {
		return nil, errors.New("scale: no fields to scale: add a field with its statistics, or paste stats")
	}

	spec := &scaleSpec{clip: evaluator.ToBool(config["clip"]), skip: skip}
	for _, row := range rows {
		f, err := parseScaleRow(row, method, stats)
		if err != nil {
			return nil, err
		}
		spec.fields = append(spec.fields, f)
	}
	return spec, nil
}

// parseStatsBlob reads the pasted statistics: {"<field>": {min, max, mean, std}}.
func parseStatsBlob(v any) (map[string]fittedStats, error) {
	const shape = "scale: stats must be a JSON object of field to {min, max, mean, std}"
	raw, err := jsonValue(v)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", shape, err)
	}
	stats := map[string]fittedStats{}
	if raw == nil {
		return stats, nil
	}
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil, errors.New(shape)
	}
	for field, entry := range obj {
		m, ok := entry.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("scale: stats for %q must be an object of min, max, mean, std", field)
		}
		st, err := readStats(m)
		if err != nil {
			return nil, fmt.Errorf("scale: stats for %q: %w", field, err)
		}
		stats[field] = st
	}
	return stats, nil
}

// parseScaleRow reads one field's rescaling. The row's own numbers win over
// the pasted stats.
func parseScaleRow(row map[string]any, method string, stats map[string]fittedStats) (scaleField, error) {
	field := core.GetConfigString(row, "field")
	if field == "" {
		return scaleField{}, errors.New("scale: every row needs a field")
	}
	target, err := evaluator.OutputField(field, core.GetConfigString(row, "targetField"), "_scaled")
	if err != nil {
		return scaleField{}, fmt.Errorf("scale: %w", err)
	}
	if m := core.GetConfigString(row, "method"); m != "" {
		method = m
	}
	own, err := readStats(row)
	if err != nil {
		return scaleField{}, fmt.Errorf("scale: %s: %w", field, err)
	}
	st := stats[field]
	f := scaleField{field: field, target: target}
	switch method {
	case methodMinMax:
		lo, hi := cmp.Or(own.Min, st.Min), cmp.Or(own.Max, st.Max)
		if lo == nil || hi == nil {
			return f, fmt.Errorf("scale: %s: min-max needs min and max", field)
		}
		if *hi <= *lo {
			return f, fmt.Errorf("scale: %s: max (%v) must be greater than min (%v)", field, *hi, *lo)
		}
		f.minmax, f.offset, f.span = true, *lo, *hi-*lo
	case methodZScore:
		mean, std := cmp.Or(own.Mean, st.Mean), cmp.Or(own.Std, st.Std)
		if mean == nil || std == nil {
			return f, fmt.Errorf("scale: %s: z-score needs mean and std", field)
		}
		if *std <= 0 {
			return f, fmt.Errorf("scale: %s: std must be greater than 0, not %v", field, *std)
		}
		f.offset, f.span = *mean, *std
	default:
		return f, fmt.Errorf("scale: %s: method must be %q or %q, not %q", field, methodMinMax, methodZScore, method)
	}
	return f, nil
}

// readStats reads whichever of min, max, mean and std m holds.
func readStats(m map[string]any) (fittedStats, error) {
	var st fittedStats
	for key, dst := range map[string]**float64{"min": &st.Min, "max": &st.Max, "mean": &st.Mean, "std": &st.Std} {
		v, ok := m[key]
		if !ok || v == nil || v == "" {
			continue
		}
		f, ok := evaluator.ToFloat64(v)
		if !ok || math.IsNaN(f) || math.IsInf(f, 0) {
			return st, fmt.Errorf("%s must be a number, not %v", key, v)
		}
		*dst = &f
	}
	return st, nil
}

// numberAt reads field as a finite number. A field or expression that is
// missing, not numeric, NaN or infinite reads as absent.
func numberAt(msg hermod.Message, field string) (float64, bool) {
	v, ok := evaluator.ToFloat64(evaluator.EvaluateField(msg, field))
	if !ok || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

// missingErr says which field a record lacked, and what it held instead.
func missingErr(node, field string, msg hermod.Message) error {
	if v := evaluator.EvaluateField(msg, field); v != nil {
		return fmt.Errorf("%s: %q is not a number (%v)", node, field, v)
	}
	return fmt.Errorf("%s: the record has no field %q", node, field)
}

// onMissingSkip reads onMissing: "fail" (the default) or "skip".
func onMissingSkip(node string, config map[string]any) (bool, error) {
	switch v := core.GetConfigString(config, "onMissing"); v {
	case "", "fail":
		return false, nil
	case "skip":
		return true, nil
	default:
		return false, fmt.Errorf("%s: onMissing must be \"fail\" or \"skip\", not %q", node, v)
	}
}

// jsonValue returns v, or what its JSON text holds. Blank text is nil.
func jsonValue(v any) (any, error) {
	s, ok := v.(string)
	if !ok {
		return v, nil
	}
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	var out any
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// objectList reads a list of objects, as JSON or JSON text.
func objectList(v any) ([]map[string]any, error) {
	raw, err := jsonValue(v)
	if err != nil {
		return nil, fmt.Errorf("must be a JSON list of objects: %w", err)
	}
	switch list := raw.(type) {
	case nil:
		return nil, nil
	case []map[string]any:
		return list, nil
	case []any:
		out := make([]map[string]any, 0, len(list))
		for i, item := range list {
			m, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("item %d must be an object, not %T", i+1, item)
			}
			out = append(out, m)
		}
		return out, nil
	}
	return nil, fmt.Errorf("must be a list of objects, not %T", raw)
}

// textList reads a list of values as text: a JSON list, JSON list text, or
// text separated by commas or new lines. A number is its shortest form, so 2
// and 2.0 are both "2".
func textList(v any) ([]string, error) {
	if s, ok := v.(string); ok && !strings.HasPrefix(strings.TrimSpace(s), "[") {
		var out []string
		for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '\n' }) {
			if p := strings.TrimSpace(part); p != "" {
				out = append(out, p)
			}
		}
		return out, nil
	}
	raw, err := jsonValue(v)
	if err != nil {
		return nil, fmt.Errorf("must be a JSON list: %w", err)
	}
	switch list := raw.(type) {
	case nil:
		return nil, nil
	case []string:
		return list, nil
	case []any:
		out := make([]string, 0, len(list))
		for _, item := range list {
			out = append(out, text(item))
		}
		return out, nil
	}
	return nil, fmt.Errorf("must be a list, not %T", raw)
}

// numberList reads a list of finite numbers, in any form textList takes.
func numberList(v any) ([]float64, error) {
	items, err := textList(v)
	if err != nil {
		return nil, err
	}
	out := make([]float64, 0, len(items))
	for _, item := range items {
		f, ok := evaluator.ToFloat64(item)
		if !ok || math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, fmt.Errorf("%q is not a number", item)
		}
		out = append(out, f)
	}
	return out, nil
}

// text is the form a value is matched and hashed in.
func text(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}
