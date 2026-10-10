package monitor

import (
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// Drift statuses, per feature and for a whole report.
const (
	StatusOK    = "ok"
	StatusWarn  = "warn"
	StatusAlert = "alert"
)

// FeatureDrift is how far one feature's live values have moved from the
// values the model was trained on.
type FeatureDrift struct {
	Feature string `json:"feature"`
	Kind    string `json:"kind"`
	// PSI is the population stability index of the live values against the
	// training split, over the training bins plus one bin for missing values.
	PSI    float64 `json:"psi"`
	Status string  `json:"status"`
	// NullFraction and TrainingNullFraction are the share of missing values
	// live and in training, the commonest drift there is.
	NullFraction         float64 `json:"null_fraction"`
	TrainingNullFraction float64 `json:"training_null_fraction"`
}

// Report is the drift of one model version over one window of predictions.
type Report struct {
	VHost       string    `json:"vhost"`
	Model       string    `json:"model"`
	Version     string    `json:"version"`
	WindowStart time.Time `json:"window_start"`
	WindowEnd   time.Time `json:"window_end"`
	// Rows is how many predicted rows the window holds.
	Rows  int64   `json:"rows"`
	Warn  float64 `json:"warn"`
	Alert float64 `json:"alert"`
	// Status is the worst of the features'.
	Status string `json:"status"`
	// Features are ordered most drifted first.
	Features []FeatureDrift `json:"features"`
}

// psiFloor stands in for an empty bin, so a bin seen only on one side adds a
// large but finite amount rather than an infinity.
const psiFloor = 1e-4

// psi is Σ (a - e) · ln(a / e) over the bins.
func psi(expected, actual []float64) float64 {
	sum := 0.0
	for i := range expected {
		e, a := max(expected[i], psiFloor), max(actual[i], psiFloor)
		sum += (a - e) * math.Log(a/e)
	}
	return sum
}

func status(psi, warn, alert float64) string {
	switch {
	case psi >= alert:
		return StatusAlert
	case psi >= warn:
		return StatusWarn
	}
	return StatusOK
}

// histogram counts one feature's live values in its training bins. The last
// count is the missing values.
type histogram struct {
	stats  worker.FeatureStats
	index  map[string]int // categorical: value -> bin; "other" is len(Top)
	counts []int64
}

func newHistogram(st worker.FeatureStats) *histogram {
	h := &histogram{stats: st}
	if st.Kind == worker.StatsCategorical {
		h.index = make(map[string]int, len(st.Top))
		for i, c := range st.Top {
			h.index[c.Value] = i
		}
		h.counts = make([]int64, len(st.Top)+2)
		return h
	}
	h.counts = make([]int64, max(len(st.Fractions), 1)+1)
	return h
}

func (h *histogram) missing() int { return len(h.counts) - 1 }

func (h *histogram) add(v any) {
	if h.stats.Kind == worker.StatsCategorical {
		s, ok := category(v)
		switch i, top := h.index[s]; {
		case !ok:
			h.counts[h.missing()]++
		case top:
			h.counts[i]++
		default:
			h.counts[len(h.stats.Top)]++
		}
		return
	}
	x, ok := number(v)
	if !ok {
		h.counts[h.missing()]++
		return
	}
	// The first edge >= x: the worker's np.searchsorted(side="left").
	h.counts[sort.SearchFloat64s(h.stats.Edges, x)]++
}

func (h *histogram) total() int64 {
	var n int64
	for _, c := range h.counts {
		n += c
	}
	return n
}

// expected is the training distribution over the same bins as counts.
func (h *histogram) expected() []float64 {
	present := 1 - h.stats.NullFraction
	out := make([]float64, len(h.counts))
	if h.stats.Kind == worker.StatsCategorical {
		for i, c := range h.stats.Top {
			out[i] = c.Fraction * present
		}
		out[len(h.stats.Top)] = h.stats.OtherFraction * present
	} else {
		for i, fr := range h.stats.Fractions {
			out[i] = fr * present
		}
	}
	out[h.missing()] = h.stats.NullFraction
	return out
}

func (h *histogram) actual() []float64 {
	out := make([]float64, len(h.counts))
	n := h.total()
	if n == 0 {
		return out
	}
	for i, c := range h.counts {
		out[i] = float64(c) / float64(n)
	}
	return out
}

func (h *histogram) drift(feature string, warn, alert float64) FeatureDrift {
	actual := h.actual()
	p := psi(h.expected(), actual)
	return FeatureDrift{
		Feature: feature, Kind: h.stats.Kind, PSI: p, Status: status(p, warn, alert),
		NullFraction: actual[h.missing()], TrainingNullFraction: h.stats.NullFraction,
	}
}

// number reads a value the way the worker feeds a numeric feature: numbers,
// booleans as 1 and 0, and numeric text. Anything else, NaN and the
// infinities are missing.
func number(v any) (float64, bool) {
	var x float64
	switch n := v.(type) {
	case float64:
		x = n
	case float32:
		x = float64(n)
	case int:
		x = float64(n)
	case int64:
		x = float64(n)
	case int32:
		x = float64(n)
	case uint64:
		x = float64(n)
	case json.Number:
		f, err := n.Float64()
		if err != nil {
			return 0, false
		}
		x = f
	case bool:
		if n {
			x = 1
		}
	case string:
		f, err := strconv.ParseFloat(n, 64)
		if err != nil {
			return 0, false
		}
		x = f
	default:
		return 0, false
	}
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return 0, false
	}
	return x, true
}

// category renders a value as the worker's to_text does, so a live value
// meets the same category it was counted as in training. Null and "" are
// missing: the worker feeds a missing string feature as "".
func category(v any) (string, bool) {
	var s string
	switch c := v.(type) {
	case nil:
		return "", false
	case string:
		s = c
	case bool:
		s = strconv.FormatBool(c)
	case float64:
		s = formatFloat(c)
	case float32:
		s = formatFloat(float64(c))
	case int:
		s = strconv.Itoa(c)
	case int64:
		s = strconv.FormatInt(c, 10)
	case json.Number:
		if f, err := c.Float64(); err == nil {
			s = formatFloat(f)
		} else {
			s = c.String()
		}
	default:
		raw, err := json.Marshal(c)
		if err != nil {
			return "", false
		}
		s = string(raw)
	}
	return s, s != ""
}

func formatFloat(f float64) string {
	if f == math.Trunc(f) && math.Abs(f) < 1<<53 {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}
