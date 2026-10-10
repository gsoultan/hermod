package features

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"slices"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/transformer"
	"github.com/gsoultan/hermod/pkg/comm/transformer/core"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
)

func init() {
	transformer.Register("encode", &Encode{})
}

// Encode turns a categorical field into numbers.
//
// Config:
//   - field: the field or expression to encode. Required.
//   - method (default "onehot"):
//   - "onehot": one 0/1 field per entry of categories, named prefix+category
//     (prefix defaults to <field>_), plus prefix+"other" for a value not in
//     the list unless otherBucket is false.
//   - "label": the integer mapping gives the value, written to targetField
//     (default <field>_label). A value not in the mapping gets unknownValue
//     (default -1), or fails the record when onUnknown is "fail".
//   - "hash": FNV-1a (32-bit) of the value modulo buckets, written to
//     targetField (default <field>_bucket). Stable across restarts and
//     versions, so training and inference agree without a vocabulary.
//   - onMissing: "fail" (default) or "skip" a record without the field.
//
// A value is matched in its text form: the number 2 matches the category "2".
type Encode struct{}

const otherBucket = "other"

type encodeSpec struct {
	field  string
	method string
	skip   bool

	// onehot
	categories []string
	columns    []string // prefix+category, in the order of categories
	other      string   // "" when there is no other bucket

	// label
	mapping     map[string]int
	unknown     int
	failUnknown bool

	// hash
	buckets uint32

	// label and hash
	target string
}

const encodeCacheKey = "_parsed_encode"

func (e *Encode) Prepare(config map[string]any) (map[string]any, error) {
	spec, err := parseEncode(config)
	if err != nil {
		return config, err
	}
	config[encodeCacheKey] = spec
	return config, nil
}

func (e *Encode) Transform(_ context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}
	spec, ok := config[encodeCacheKey].(*encodeSpec)
	if !ok {
		var err error
		if spec, err = parseEncode(config); err != nil {
			return msg, err
		}
	}
	raw := evaluator.EvaluateField(msg, spec.field)
	if raw == nil {
		if spec.skip {
			return msg, nil
		}
		return msg, fmt.Errorf("encode: the record has no field %q", spec.field)
	}
	value := text(raw)

	switch spec.method {
	case "onehot":
		hit := slices.Index(spec.categories, value)
		for i, column := range spec.columns {
			msg.SetData(column, boolInt(i == hit))
		}
		if spec.other != "" {
			msg.SetData(spec.other, boolInt(hit < 0))
		}
	case "label":
		code, ok := spec.mapping[value]
		if !ok {
			if spec.failUnknown {
				return msg, fmt.Errorf("encode: %q has no label for %q", spec.field, value)
			}
			code = spec.unknown
		}
		msg.SetData(spec.target, code)
	case "hash":
		h := fnv.New32a()
		_, _ = h.Write([]byte(value))
		msg.SetData(spec.target, int(h.Sum32()%spec.buckets))
	}
	return msg, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// maxHashBuckets bounds a hash encoding; more buckets than this is not a
// feature a model can use.
const maxHashBuckets = 1 << 24

func parseEncode(config map[string]any) (*encodeSpec, error) {
	spec := &encodeSpec{field: core.GetConfigString(config, "field"), method: core.GetConfigString(config, "method")}
	if spec.field == "" {
		return nil, errors.New("encode: choose a field to encode")
	}
	var err error
	if spec.skip, err = onMissingSkip("encode", config); err != nil {
		return nil, err
	}
	switch spec.method {
	case "", "onehot":
		spec.method = "onehot"
		err = spec.parseOneHot(config)
	case "label":
		err = spec.parseLabel(config)
	case "hash":
		err = spec.parseHash(config)
	default:
		err = fmt.Errorf("encode: method must be \"onehot\", \"label\" or \"hash\", not %q", spec.method)
	}
	if err != nil {
		return nil, err
	}
	return spec, nil
}

func (spec *encodeSpec) parseOneHot(config map[string]any) error {
	var err error
	if spec.categories, err = textList(config["categories"]); err != nil {
		return fmt.Errorf("encode: categories %w", err)
	}
	if len(spec.categories) == 0 {
		return errors.New("encode: one-hot needs a list of categories")
	}
	prefix, err := evaluator.OutputField(spec.field, core.GetConfigString(config, "prefix"), "_")
	if err != nil {
		return fmt.Errorf("encode: %w (the prefix of the one-hot fields)", err)
	}
	withOther := config["otherBucket"] == nil || evaluator.ToBool(config["otherBucket"])
	seen := map[string]bool{}
	for _, c := range spec.categories {
		if seen[c] {
			return fmt.Errorf("encode: category %q is listed twice", c)
		}
		if withOther && c == otherBucket {
			return fmt.Errorf("encode: category %q would share its field with the other bucket; rename it or turn the other bucket off", c)
		}
		seen[c] = true
		spec.columns = append(spec.columns, prefix+c)
	}
	if withOther {
		spec.other = prefix + otherBucket
	}
	return nil
}

func (spec *encodeSpec) parseLabel(config map[string]any) error {
	raw, err := jsonValue(config["mapping"])
	if err != nil {
		return fmt.Errorf("encode: mapping must be a JSON object of category to integer: %w", err)
	}
	obj, _ := raw.(map[string]any)
	if len(obj) == 0 {
		return errors.New("encode: label encoding needs a mapping of category to integer")
	}
	spec.mapping = make(map[string]int, len(obj))
	for category, v := range obj {
		n, err := wholeNumber(v)
		if err != nil {
			return fmt.Errorf("encode: mapping %q: %w", category, err)
		}
		spec.mapping[category] = n
	}
	spec.unknown = -1
	if v, ok := config["unknownValue"]; ok && v != nil && v != "" {
		if spec.unknown, err = wholeNumber(v); err != nil {
			return fmt.Errorf("encode: unknownValue: %w", err)
		}
	}
	switch on := core.GetConfigString(config, "onUnknown"); on {
	case "", "value":
	case "fail":
		spec.failUnknown = true
	default:
		return fmt.Errorf("encode: onUnknown must be \"value\" or \"fail\", not %q", on)
	}
	if spec.target, err = evaluator.OutputField(spec.field, core.GetConfigString(config, "targetField"), "_label"); err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	return nil
}

func (spec *encodeSpec) parseHash(config map[string]any) error {
	n, err := wholeNumber(config["buckets"])
	if err != nil || n < 1 || n > maxHashBuckets {
		return fmt.Errorf("encode: buckets must be a whole number from 1 to %d", maxHashBuckets)
	}
	spec.buckets = uint32(n)
	if spec.target, err = evaluator.OutputField(spec.field, core.GetConfigString(config, "targetField"), "_bucket"); err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	return nil
}

// wholeNumber reads an integer, refusing a fraction rather than rounding it.
func wholeNumber(v any) (int, error) {
	f, ok := evaluator.ToFloat64(v)
	if !ok || f != math.Trunc(f) || math.Abs(f) > math.MaxInt32 {
		return 0, fmt.Errorf("must be a whole number, not %v", v)
	}
	return int(f), nil
}
