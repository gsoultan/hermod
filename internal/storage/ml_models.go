package storage

import (
	"context"
	"errors"
	"fmt"
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

// maxMLFeatureTypeLen bounds one feature's type, which the worker reports as
// number, string or bool.
const maxMLFeatureTypeLen = 64

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
	if len(m.FeatureTypes) > maxMLModelFeatures {
		return fmt.Errorf("a model may type at most %d features", maxMLModelFeatures)
	}
	for f, t := range m.FeatureTypes {
		if len(t) > maxMLFeatureTypeLen {
			return fmt.Errorf("feature %q has a type longer than %d characters", f, maxMLFeatureTypeLen)
		}
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
