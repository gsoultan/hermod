package storage

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gsoultan/hermod/pkg/ml/inference"
)

// MLModel is a model a vhost can call: where it is served and how to send it
// rows. The model itself lives on a model server (hermod-ml, KServe, Triton,
// MLServer, MLflow); Hermod keeps the address and calls it from the Predict
// node and the serving endpoints.
type MLModel struct {
	VHost       string `json:"vhost"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`

	// Backend, URL, RemoteModel and RemoteVersion are inference.Target's
	// Backend, URL, Model and Version.
	Backend       inference.Backend `json:"backend"`
	URL           string            `json:"url"`
	RemoteModel   string            `json:"remote_model,omitempty"`
	RemoteVersion string            `json:"remote_version,omitempty"`

	// TokenSecret names a secret of the same vhost whose value is sent as a
	// bearer token. The token itself is never stored on the model.
	TokenSecret string `json:"token_secret,omitempty"`

	InputName string   `json:"input_name,omitempty"`
	Features  []string `json:"features,omitempty"`
	TimeoutMs int      `json:"timeout_ms,omitempty"`

	// ServingKeyHash is the SHA-256 of the key an application presents to call
	// the model through the serving endpoints. Empty means serving is off. The
	// key itself is shown once, when it is made, and never stored.
	ServingKeyHash string `json:"-"`
	// Serving reports whether a serving key is set; it is what the API shows.
	Serving bool `json:"serving"`

	// Monitoring is how the model's predictions are logged and its drift
	// judged. The zero value logs nothing and uses the default thresholds.
	Monitoring MLMonitoring `json:"monitoring"`

	UpdatedBy string    `json:"updated_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// MLBackendWorker marks a model trained by Hermod's own hermod-ml worker. Its
// server is the worker Hermod is configured with, so it holds no URL; its
// features are its inputs, one tensor each; and RemoteVersion is the version
// that is live, empty until one is put live.
const MLBackendWorker inference.Backend = "hermod-ml"

// MaxMLModelNameLen bounds a model name, which appears in URLs and metrics.
const MaxMLModelNameLen = 64

// maxMLModelFeatures bounds the feature list a model may declare.
const maxMLModelFeatures = 4096

// ErrMLModelsUnsupported is returned when the configured backend cannot store
// models.
var ErrMLModelsUnsupported = errors.New("this storage backend cannot hold ML models")

// ValidMLModelName reports whether name is a letter followed by letters,
// digits, '_' or '-', within the length limit. It is used in URL paths, so it
// may hold nothing that changes a path's meaning.
func ValidMLModelName(name string) bool {
	if name == "" || len(name) > MaxMLModelNameLen {
		return false
	}
	for i, c := range name {
		letter := (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
		digit := c >= '0' && c <= '9'
		if !letter && (i == 0 || (!digit && c != '_' && c != '-')) {
			return false
		}
	}
	return true
}

// Target is the call this model makes, with the bearer token supplied by the
// caller (it is looked up from TokenSecret, never held here).
func (m MLModel) Target(token string) inference.Target {
	return inference.Target{
		Backend:   m.Backend,
		URL:       m.URL,
		Model:     m.RemoteModel,
		Version:   m.RemoteVersion,
		Token:     token,
		InputName: m.InputName,
		Features:  m.Features,
		Timeout:   time.Duration(m.TimeoutMs) * time.Millisecond,
	}
}

// ValidateMLModel reports what is wrong with a model before it is saved.
func ValidateMLModel(m MLModel) error {
	if m.VHost == "" {
		return errors.New("a model belongs to one vhost: name it")
	}
	if !ValidMLModelName(m.Name) {
		return fmt.Errorf("model name %q must start with a letter and hold only letters, digits, '_' or '-' (at most %d)", m.Name, MaxMLModelNameLen)
	}
	if m.TokenSecret != "" && !ValidVHostSecretName(m.TokenSecret) {
		return fmt.Errorf("token secret %q is not a valid secret name", m.TokenSecret)
	}
	if m.TimeoutMs < 0 || m.TimeoutMs > 10*60*1000 {
		return errors.New("timeout_ms must be between 0 and 600000")
	}
	if len(m.Features) > maxMLModelFeatures {
		return fmt.Errorf("a model may declare at most %d features", maxMLModelFeatures)
	}
	if err := m.Monitoring.Validate(); err != nil {
		return err
	}
	if m.Backend == MLBackendWorker {
		return validateWorkerModel(m)
	}
	return m.Target("").Validate()
}

func validateWorkerModel(m MLModel) error {
	if m.URL != "" {
		return errors.New("a model trained by Hermod is served by its ML worker: it takes no URL")
	}
	if m.InputName != "" {
		return errors.New("a model trained by Hermod takes one input per feature, not an input name")
	}
	if m.RemoteVersion != "" && !validVersion(m.RemoteVersion) {
		return fmt.Errorf("version %q must be a version number", m.RemoteVersion)
	}
	return nil
}

// validVersion accepts the worker's version numbers.
func validVersion(v string) bool {
	if v == "" || len(v) > 12 {
		return false
	}
	for _, c := range v {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// MLModelStore is implemented by a storage backend that can hold models per
// vhost. Like VHostSecretStore it is separate from Storage; callers find it
// with a type assertion.
type MLModelStore interface {
	// ListMLModels returns the vhost's models ordered by name.
	ListMLModels(ctx context.Context, vhost string) ([]MLModel, error)
	// GetMLModel returns one model, or ErrNotFound.
	GetMLModel(ctx context.Context, vhost, name string) (MLModel, error)
	// PutMLModel creates the model or replaces its definition. It never
	// changes the serving key; SetMLModelServingKey does.
	PutMLModel(ctx context.Context, m MLModel) error
	// SetMLModelServingKey stores the hash of a new serving key, or turns
	// serving off when hash is empty. It returns ErrNotFound for no such model.
	SetMLModelServingKey(ctx context.Context, vhost, name, hash string) error
	// DeleteMLModel removes one model, or returns ErrNotFound.
	DeleteMLModel(ctx context.Context, vhost, name string) error
	// DeleteMLModels removes every model the vhost holds.
	DeleteMLModels(ctx context.Context, vhost string) error
}

// Defaults and bounds of a model's monitoring.
const (
	DefaultMLDriftWarn    = 0.1
	DefaultMLDriftAlert   = 0.25
	DefaultMLLogRetention = 7 * 24 * time.Hour

	// MaxMLLogRetention is the longest a logged prediction is kept, whatever
	// its model says; the retention sweep applies it to every row, so the
	// log of a model or vhost deleted elsewhere does not outlive it.
	MaxMLLogRetention = 365 * 24 * time.Hour

	minMLLogRetention  = time.Hour
	maxMLLogMaskFields = 100
)

// MLMaskTypes are the ways a logged field can be masked: the mask
// transformer's (pkg/comm/transformer/security), "all" replacing the value.
var MLMaskTypes = []string{"all", "partial", "email", "pii"}

// MLMonitoring is how a model is watched.
//
// Prediction logging is off until LogSampleRate is above zero: a logged row
// holds what the model was sent, which may be personal data, so it is never
// kept unless someone asks for it. LogMaskFields are masked before a row is
// written, never after.
type MLMonitoring struct {
	// LogSampleRate is the share of predictions logged, 0 to 1.
	LogSampleRate float64 `json:"log_sample_rate,omitempty"`
	// LogMaskFields are input or output fields masked in a logged row, as
	// dotted paths; "*" masks every string.
	LogMaskFields []string `json:"log_mask_fields,omitempty"`
	// LogMaskType is one of MLMaskTypes; empty means "all".
	LogMaskType string `json:"log_mask_type,omitempty"`
	// LogRetention is how long a logged row is kept, as "7d" or "12h"; empty
	// means DefaultMLLogRetention.
	LogRetention string `json:"log_retention,omitempty"`

	// DriftWarn and DriftAlert are the population stability index at which a
	// feature is reported as drifting, and at which an alert is sent; zero
	// means the default.
	DriftWarn  float64 `json:"drift_warn,omitempty"`
	DriftAlert float64 `json:"drift_alert,omitempty"`
}

// Logging reports whether any prediction is logged.
func (m MLMonitoring) Logging() bool { return m.LogSampleRate > 0 }

// Thresholds returns the drift thresholds, defaults filled in.
func (m MLMonitoring) Thresholds() (warn, alert float64) {
	warn, alert = m.DriftWarn, m.DriftAlert
	if warn == 0 {
		warn = DefaultMLDriftWarn
	}
	if alert == 0 {
		alert = DefaultMLDriftAlert
	}
	return warn, alert
}

// Retention returns how long a logged prediction is kept. An unreadable
// value, which Validate refuses, is read as the default.
func (m MLMonitoring) Retention() time.Duration {
	d, err := parseRetention(m.LogRetention)
	if err != nil || d == 0 {
		return DefaultMLLogRetention
	}
	return d
}

// Validate reports what is wrong with the setting.
func (m MLMonitoring) Validate() error {
	if m.LogSampleRate < 0 || m.LogSampleRate > 1 {
		return errors.New("the prediction log sample rate is between 0 and 1")
	}
	if m.LogMaskType != "" && !slices.Contains(MLMaskTypes, m.LogMaskType) {
		return fmt.Errorf("mask type %q is one of %s", m.LogMaskType, strings.Join(MLMaskTypes, ", "))
	}
	if len(m.LogMaskFields) > maxMLLogMaskFields {
		return fmt.Errorf("at most %d fields can be masked", maxMLLogMaskFields)
	}
	for _, f := range m.LogMaskFields {
		if strings.TrimSpace(f) == "" {
			return errors.New("a masked field needs a name")
		}
	}
	if m.LogRetention != "" {
		d, err := parseRetention(m.LogRetention)
		if err != nil {
			return fmt.Errorf("prediction log retention %q is not a duration such as 7d or 12h", m.LogRetention)
		}
		if d < minMLLogRetention || d > MaxMLLogRetention {
			return errors.New("prediction log retention is between 1h and 365d")
		}
	}
	if m.DriftWarn < 0 || m.DriftAlert < 0 {
		return errors.New("drift thresholds cannot be negative")
	}
	if warn, alert := m.Thresholds(); alert < warn {
		return fmt.Errorf("the drift alert threshold %.3g is below the warning threshold %.3g", alert, warn)
	}
	return nil
}

// parseRetention reads "7d" as days and anything else as a Go duration,
// the way workflow trace retention is written.
func parseRetention(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	if days, ok := strings.CutSuffix(s, "d"); ok {
		f, err := strconv.ParseFloat(days, 64)
		if err != nil {
			return 0, err
		}
		return time.Duration(f * float64(24*time.Hour)), nil
	}
	return time.ParseDuration(s)
}

// Who made a prediction.
const (
	MLCallerWorkflow = "workflow" // a Predict node; CallerID is the workflow
	MLCallerREST     = "rest"     // the serving endpoint, with a serving key
	MLCallerGRPC     = "grpc"     // hermod.ml.v1.InferenceService
	MLCallerUI       = "ui"       // an Editor testing the model
)

// MLPredictionLog is one logged prediction: one row a model was sent and
// what it answered. Inputs hold the row after masking.
type MLPredictionLog struct {
	VHost     string         `json:"vhost"`
	Model     string         `json:"model"`
	Version   string         `json:"version,omitempty"`
	Timestamp time.Time      `json:"timestamp"`
	Inputs    map[string]any `json:"inputs"`
	Outputs   map[string]any `json:"outputs"`
	// LatencyMs is how long the whole call, of which this row was part, took.
	LatencyMs  float64 `json:"latency_ms"`
	CallerKind string  `json:"caller_kind"`
	CallerID   string  `json:"caller_id,omitempty"`
}

// MaxMLPredictionLogPage bounds one read of a model's prediction log.
const MaxMLPredictionLogPage = 500

// ErrMLPredictionLogsUnsupported is returned when the log store cannot hold
// prediction logs.
var ErrMLPredictionLogsUnsupported = errors.New("this storage backend cannot hold ML prediction logs")

// MLPredictionLogStore is implemented by a log store that can keep a
// model's logged predictions. Like MLModelStore, callers find it with a type
// assertion.
type MLPredictionLogStore interface {
	// InsertMLPredictionLogs writes a batch of logged predictions.
	InsertMLPredictionLogs(ctx context.Context, logs []MLPredictionLog) error
	// ListMLPredictionLogs returns a model's most recent logged predictions,
	// newest first, at most limit (bounded by MaxMLPredictionLogPage).
	ListMLPredictionLogs(ctx context.Context, vhost, model string, limit int) ([]MLPredictionLog, error)
	// PurgeMLPredictionLogs removes logged predictions older than before: of
	// one model, of every model of the vhost when model is empty, or of
	// everything when vhost is empty too.
	PurgeMLPredictionLogs(ctx context.Context, vhost, model string, before time.Time) error
	// DeleteMLPredictionLogs removes every logged prediction of a model, or of
	// every model of the vhost when model is empty.
	DeleteMLPredictionLogs(ctx context.Context, vhost, model string) error
}

// MLPredictionLogLimit bounds a requested page size.
func MLPredictionLogLimit(limit int) int {
	if limit <= 0 || limit > MaxMLPredictionLogPage {
		return MaxMLPredictionLogPage
	}
	return limit
}
