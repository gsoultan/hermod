package features

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/transformer"
	"github.com/gsoultan/hermod/pkg/comm/transformer/core"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
)

func init() {
	transformer.Register("anomaly_score", &AnomalyScore{})
}

// AnomalyScore scores a field against its recent values, per key, and flags
// the records that stand out.
//
// Config: field (required), keyBy, windowType, size, window, maxEvents,
// maxKeys, persistent and onMissing as for rolling, plus:
//   - method: "zscore" (default) scores |v - mean| / std; "iqr" scores how far
//     v lies outside [Q1, Q3], in IQRs (0 inside). Quartiles interpolate
//     linearly, as numpy's default does.
//   - threshold: a score above it is an anomaly. Default 3 for zscore, 1.5
//     (Tukey's fences) for iqr.
//   - minEvents: the history a key needs before it is scored (default 10, or
//     the window size when that is smaller). Until then the score is null and
//     the flag false.
//   - scoreField, flagField: default <field>_anomaly_score and
//     <field>_is_anomaly.
//
// A record is scored against the window before it, then joins it. A history
// with no spread (every value equal) scores an equal value 0 and flags any
// other with a null score, since no finite score exists.
//
// State is kept, bounded and restored exactly as rolling's is.
type AnomalyScore struct {
	now     func() time.Time
	windows windowStore
}

type anomalySpec struct {
	win                   windowSpec
	iqr                   bool
	threshold             float64
	minEvents             int
	scoreField, flagField string
}

const (
	anomalyCacheKey  = "_parsed_anomaly"
	defaultMinEvents = 10
)

func (a *AnomalyScore) Prepare(config map[string]any) (map[string]any, error) {
	spec, err := parseAnomaly(config)
	if err != nil {
		return config, err
	}
	config[anomalyCacheKey] = spec
	return config, nil
}

func (a *AnomalyScore) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}
	spec, ok := config[anomalyCacheKey].(*anomalySpec)
	if !ok {
		var err error
		if spec, err = parseAnomaly(config); err != nil {
			return msg, err
		}
	}
	v, ok := numberAt(msg, spec.win.field)
	if !ok {
		if spec.win.skip {
			return msg, nil
		}
		return msg, missingErr("anomaly_score", spec.win.field, msg)
	}
	prior := a.windows.record(ctx, "anomaly", &spec.win, msg, v, clockNow(a.now))

	var score any
	flagged := false
	if len(prior) >= spec.minEvents {
		var s float64
		var finite bool
		if spec.iqr {
			s, finite = iqrScore(prior, v)
		} else {
			s, finite = zScore(prior, v)
		}
		if finite {
			score, flagged = s, s > spec.threshold
		} else {
			flagged = true
		}
	}
	msg.SetData(spec.scoreField, score)
	msg.SetData(spec.flagField, flagged)
	return msg, nil
}

// zScore is |v - mean| / std over prior. finite is false when prior has no
// spread and v differs from it.
func zScore(prior []float64, v float64) (score float64, finite bool) {
	s := summarize(prior)
	d := math.Abs(v - s.mean)
	if s.std == 0 {
		return 0, d == 0
	}
	return d / s.std, true
}

// iqrScore is how many IQRs v lies outside [Q1, Q3] of prior.
func iqrScore(prior []float64, v float64) (score float64, finite bool) {
	sorted := slices.Clone(prior)
	slices.Sort(sorted)
	q1, q3 := quantile(sorted, 0.25), quantile(sorted, 0.75)
	var d float64
	switch {
	case v < q1:
		d = q1 - v
	case v > q3:
		d = v - q3
	default:
		return 0, true
	}
	if q3 == q1 {
		return 0, false
	}
	return d / (q3 - q1), true
}

// quantile interpolates linearly between the closest ranks of sorted.
func quantile(sorted []float64, q float64) float64 {
	pos := q * float64(len(sorted)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	return sorted[lo] + (pos-float64(lo))*(sorted[hi]-sorted[lo])
}

// parseMethod reads method and threshold, whose default depends on it.
func (spec *anomalySpec) parseMethod(config map[string]any) error {
	spec.threshold = 3
	switch m := core.GetConfigString(config, "method"); m {
	case "", "zscore":
	case "iqr":
		spec.iqr, spec.threshold = true, 1.5
	default:
		return fmt.Errorf("anomaly_score: method must be \"zscore\" or \"iqr\", not %q", m)
	}
	if v, ok := config["threshold"]; ok && v != nil && v != "" {
		t, ok := evaluator.ToFloat64(v)
		if !ok || t <= 0 || math.IsNaN(t) || math.IsInf(t, 0) {
			return fmt.Errorf("anomaly_score: threshold must be a number above 0, not %v", v)
		}
		spec.threshold = t
	}
	return nil
}

func parseAnomaly(config map[string]any) (*anomalySpec, error) {
	if core.GetConfigString(config, "field") == "" {
		return nil, errors.New("anomaly_score: choose a field to score")
	}
	spec := &anomalySpec{}
	if err := spec.parseMethod(config); err != nil {
		return nil, err
	}
	win, err := parseWindow("anomaly_score", config)
	if err != nil {
		return nil, err
	}
	spec.win = win
	spec.minEvents = defaultMinEvents
	if !win.byTime {
		spec.minEvents = min(spec.minEvents, win.size)
	}
	if v, ok := config["minEvents"]; ok && v != nil && v != "" {
		if spec.minEvents, err = boundedInt(v, 2, maxWindowEvents); err != nil {
			return nil, fmt.Errorf("anomaly_score: minEvents %w", err)
		}
	}
	if spec.minEvents > win.size {
		return nil, fmt.Errorf("anomaly_score: minEvents (%d) is more than the window holds (%d), so nothing would be scored", spec.minEvents, win.size)
	}
	if spec.scoreField, err = evaluator.OutputField(win.field, core.GetConfigString(config, "scoreField"), "_anomaly_score"); err != nil {
		return nil, fmt.Errorf("anomaly_score: %w", err)
	}
	if spec.flagField, err = evaluator.OutputField(win.field, core.GetConfigString(config, "flagField"), "_is_anomaly"); err != nil {
		return nil, fmt.Errorf("anomaly_score: %w", err)
	}
	return spec, nil
}
