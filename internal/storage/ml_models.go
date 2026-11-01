// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

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
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// MLModel is a model a vhost can call: where it is served and how to send it
// rows. The model itself lives on a model server (hermod-ml, mimir-server,
// KServe, Triton, MLServer, MLflow); Hermod keeps the address and calls it
// from the Predict node and the serving endpoints. For a mimir server
// RemoteModel is the model's id there and TokenSecret holds its API key.
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
	// FeatureTypes is feature -> number, string or bool, where it is known:
	// for a model Hermod trained, from the dataset it was trained on.
	FeatureTypes map[string]string `json:"feature_types,omitempty"`
	TimeoutMs    int               `json:"timeout_ms,omitempty"`

	// MCPExposed offers the model as a predict_<name> tool on Hermod's MCP
	// server to callers who may use its vhost. Off unless someone turns it on.
	MCPExposed bool `json:"mcp_exposed"`

	// ServingKeyHash is the SHA-256 of the key an application presents to call
	// the model through the serving endpoints. Empty means serving is off. The
	// key itself is shown once, when it is made, and never stored.
	ServingKeyHash string `json:"-"`
	// Serving reports whether a serving key is set; it is what the API shows.
	Serving bool `json:"serving"`

	// Monitoring is how the model's predictions are logged and its drift
	// judged. The zero value logs nothing and uses the default thresholds.
	Monitoring MLMonitoring `json:"monitoring"`
	// Retrain is when a trained model trains again by itself, nil for never.
	// RetrainStatus is what its last training did. PutMLModel changes
	// neither; MLRetrainStore does.
	Retrain       *MLRetrainPolicy `json:"retrain,omitempty"`
	RetrainStatus *MLRetrainStatus `json:"retrain_status,omitempty"`

	// Scoring is where a trained model's predictions are computed:
	// MLScoringWorker (also the meaning of empty) or MLScoringInProcess.
	// PutMLModel does not change it; MLScoringStore does.
	Scoring string `json:"scoring,omitempty"`

	// Task is what a model Hermod trained does, as its live version (or,
	// before one is live, its latest) says: classification, regression or
	// MLTaskRecommendation. A recommender is called with a person or an
	// item, not rows. Empty for a model served elsewhere, and for one
	// trained before the task was kept.
	Task string `json:"task,omitempty"`

	UpdatedBy string    `json:"updated_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// MLBackendWorker marks a model trained by Hermod's own hermod-ml worker. Its
// server is the worker Hermod is configured with, so it holds no URL; its
// features are its inputs, one tensor each; and RemoteVersion is the version
// that is live, empty until one is put live.
const MLBackendWorker inference.Backend = "hermod-ml"

// Where a trained model is scored. In-process scoring evaluates the live
// version's ONNX graph inside Hermod (pkg/ml/onnxscore) when every operator
// in it is supported, and falls back to the worker when not.
const (
	MLScoringWorker    = "worker"
	MLScoringInProcess = "in_process"
)

// ValidateMLScoring reports whether s is a scoring mode; empty is the
// default, the worker.
func ValidateMLScoring(s string) error {
	switch s {
	case "", MLScoringWorker, MLScoringInProcess:
		return nil
	}
	return fmt.Errorf("scoring is %q or %q, not %q", MLScoringWorker, MLScoringInProcess, s)
}

// MLTaskRecommendation is the task of a trained model that recommends items
// to people. Hermod serves it in-process from its artifact (pkg/ml/recommend).
const MLTaskRecommendation = "recommendation"

// IsRecommender reports whether the model recommends items rather than
// predicting a column.
func (m MLModel) IsRecommender() bool { return m.Task == MLTaskRecommendation }

// MaxMLModelNameLen bounds a model name, which appears in URLs and metrics.
const MaxMLModelNameLen = 64

// maxMLModelFeatures bounds the feature list a model may declare.
const maxMLModelFeatures = 4096

// maxMLFeatureTypeLen bounds one feature's type, which the worker reports as
// number, string or bool.
const maxMLFeatureTypeLen = 64

// ErrMLModelsUnsupported is returned when the configured backend cannot store
// models.
var ErrMLModelsUnsupported = errors.New("this storage backend cannot hold ML models")

// MLWorkerSettingKey names the settings row holding the ML worker connected
// from the console: a JSON object of strings, "url" and "token", whose token
// is encrypted as a connector's credentials are (configsecrets).
const MLWorkerSettingKey = "hermod.ml_worker"

// EncryptedSettingKeys are the settings whose value is a JSON object of
// strings with its credentials encrypted by configsecrets. ReEncryptSecrets
// rewrites them with a connector's config, so rotating the master key does not
// strand them.
var EncryptedSettingKeys = []string{MLWorkerSettingKey, EnvironmentLinkTokensKey}

// EnvironmentLinkTokensKey names the settings row holding the tokens this
// instance presents to the environments it promotes to (internal/environment):
// a JSON object of strings, one "<link id>_token" each, encrypted.
const EnvironmentLinkTokensKey = "hermod.environment_link_tokens" //nolint:gosec // G101: the name of a setting, not a credential.

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
	if len(m.FeatureTypes) > maxMLModelFeatures {
		return fmt.Errorf("a model may type at most %d features", maxMLModelFeatures)
	}
	for f, t := range m.FeatureTypes {
		if len(t) > maxMLFeatureTypeLen {
			return fmt.Errorf("feature %q has a type longer than %d characters", f, maxMLFeatureTypeLen)
		}
	}
	if err := m.Monitoring.Validate(); err != nil {
		return err
	}
	switch m.Task {
	case "", "classification", "regression":
	case MLTaskRecommendation:
		if m.Backend != MLBackendWorker {
			return errors.New("only a model trained by Hermod can be a recommendation model")
		}
	default:
		return fmt.Errorf("task %q is not classification, regression or %s", m.Task, MLTaskRecommendation)
	}
	if m.Backend == MLBackendWorker {
		return validateWorkerModel(m)
	}
	if m.Backend == inference.BackendMimir && m.TokenSecret == "" {
		return errors.New("a mimir server answers no one without an API key: name the secret that holds it")
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

// validVersion accepts the worker's version numbers (1, 2, …) and the
// built-in engine's (b1, b2, …).
func validVersion(v string) bool {
	if v == "" || len(v) > 12 {
		return false
	}
	if worker.IsBuiltinVersion(v) {
		return true
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

// MLScoringStore is implemented by a storage backend that keeps a trained
// model's scoring mode with it. Like MLRetrainStore, callers find it with a
// type assertion.
type MLScoringStore interface {
	// SetMLModelScoring stores where the model is scored. It returns
	// ErrNotFound for no such model.
	SetMLModelScoring(ctx context.Context, vhost, name, scoring string) error
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
	MLCallerMCP      = "mcp"      // a predict_<model> tool on the MCP server
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
