package features

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/transformer"
	"github.com/gsoultan/hermod/pkg/comm/transformer/core"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
)

func init() {
	transformer.Register("bucketize", &Bucketize{})
}

// Bucketize puts a numeric field into a bin.
//
// Config:
//   - field: the field or expression to read. Required.
//   - edges: at least two strictly increasing numbers. Edges e0..en make n
//     bins: [e0, e1), [e1, e2), ..., [e(n-1), en] -- each bin holds its lower
//     edge, and the last also holds en.
//   - labels: n names for the bins. Without them the bin's index (from 0) is
//     written.
//   - targetField: default <field>_bin.
//   - outOfRange: for a value below e0 or above en, "null" (default) writes
//     null, "clip" uses the first or last bin, "fail" fails the record.
//   - onMissing: "fail" (default) or "skip" a record without a numeric value.
type Bucketize struct{}

type bucketSpec struct {
	field, target string
	edges         []float64
	labels        []string
	outOfRange    string
	skip          bool
}

const bucketCacheKey = "_parsed_bucketize"

func (b *Bucketize) Prepare(config map[string]any) (map[string]any, error) {
	spec, err := parseBucketize(config)
	if err != nil {
		return config, err
	}
	config[bucketCacheKey] = spec
	return config, nil
}

func (b *Bucketize) Transform(_ context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}
	spec, ok := config[bucketCacheKey].(*bucketSpec)
	if !ok {
		var err error
		if spec, err = parseBucketize(config); err != nil {
			return msg, err
		}
	}
	v, ok := numberAt(msg, spec.field)
	if !ok {
		if spec.skip {
			return msg, nil
		}
		return msg, missingErr("bucketize", spec.field, msg)
	}

	bins := len(spec.edges) - 1
	var bin int
	switch {
	case v < spec.edges[0] || v > spec.edges[bins]:
		switch spec.outOfRange {
		case "clip":
			bin = 0
			if v > spec.edges[bins] {
				bin = bins - 1
			}
		case "fail":
			return msg, fmt.Errorf("bucketize: %q is %v, outside [%v, %v]", spec.field, v, spec.edges[0], spec.edges[bins])
		default:
			msg.SetData(spec.target, nil)
			return msg, nil
		}
	case v == spec.edges[bins]:
		bin = bins - 1
	default:
		// The first edge above v closes v's bin.
		bin = sort.SearchFloat64s(spec.edges, v)
		if bin == len(spec.edges) || spec.edges[bin] != v {
			bin--
		}
	}

	if spec.labels != nil {
		msg.SetData(spec.target, spec.labels[bin])
	} else {
		msg.SetData(spec.target, bin)
	}
	return msg, nil
}

func parseBucketize(config map[string]any) (*bucketSpec, error) {
	spec := &bucketSpec{field: core.GetConfigString(config, "field")}
	if spec.field == "" {
		return nil, errors.New("bucketize: choose a field to bucket")
	}
	var err error
	if spec.skip, err = onMissingSkip("bucketize", config); err != nil {
		return nil, err
	}
	if spec.edges, err = numberList(config["edges"]); err != nil {
		return nil, fmt.Errorf("bucketize: edges: %w", err)
	}
	if len(spec.edges) < 2 {
		return nil, errors.New("bucketize: give at least two edges, e.g. 0, 18, 65")
	}
	for i := 1; i < len(spec.edges); i++ {
		if spec.edges[i] <= spec.edges[i-1] {
			return nil, fmt.Errorf("bucketize: edges must be increasing: %v comes after %v", spec.edges[i], spec.edges[i-1])
		}
	}
	if spec.labels, err = textList(config["labels"]); err != nil {
		return nil, fmt.Errorf("bucketize: labels %w", err)
	}
	if len(spec.labels) == 0 {
		spec.labels = nil
	} else if len(spec.labels) != len(spec.edges)-1 {
		return nil, fmt.Errorf("bucketize: %d edges make %d bins, but there are %d labels", len(spec.edges), len(spec.edges)-1, len(spec.labels))
	}
	switch spec.outOfRange = core.GetConfigString(config, "outOfRange"); spec.outOfRange {
	case "", "null", "clip", "fail":
	default:
		return nil, fmt.Errorf("bucketize: outOfRange must be \"null\", \"clip\" or \"fail\", not %q", spec.outOfRange)
	}
	if spec.target, err = evaluator.OutputField(spec.field, core.GetConfigString(config, "targetField"), "_bin"); err != nil {
		return nil, fmt.Errorf("bucketize: %w", err)
	}
	return spec, nil
}
