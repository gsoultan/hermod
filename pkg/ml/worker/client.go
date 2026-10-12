// Package worker is Hermod's client for its hermod-ml worker: the Python
// process that holds datasets, trains models on them, exports each one to
// ONNX and serves it over the Open Inference Protocol.
//
// Hermod stays the source of truth for which model a vhost calls and which
// version is live; the worker holds the data and the model files. Predictions
// do not go through this package: a trained model is called like any other
// Open Inference Protocol server, through pkg/ml/inference, at ServingURL.
package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/gsoultan/hermod/pkg/infra/httpclient"
)

var (
	// ErrNotFound is returned when the worker has no such dataset, model or
	// version.
	ErrNotFound = errors.New("not found on the ML worker")
	// ErrBusy is returned when the worker is already running as many
	// trainings as it allows.
	ErrBusy = errors.New("the ML worker is busy training")
)

// Timeouts. A training is synchronous and can take minutes; everything else
// is a quick call. HERMOD_ML_TRAIN_TIMEOUT overrides the first.
const (
	DefaultTrainTimeout = 30 * time.Minute
	callTimeout         = 2 * time.Minute
	maxReplyBytes       = 16 << 20
	// maxModelFileBytes bounds a model file. A tabular model small enough to
	// score in-process is far below it; a larger one is left to the worker.
	maxModelFileBytes = 64 << 20
)

// names the worker accepts in a path; the worker checks the same rule.
var validName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

func checkName(kind, name string) error {
	if !validName.MatchString(name) || name == "." || name == ".." {
		return fmt.Errorf("%s name %q may hold only letters, digits, '_', '.' or '-'", kind, name)
	}
	return nil
}

// Client calls one hermod-ml worker.
type Client struct {
	// URL is the worker's base URL, without a trailing slash.
	URL string
	// Token, when set, is sent as a bearer token on every call.
	Token string
	// TrainTimeout bounds one training call.
	TrainTimeout time.Duration

	http *http.Client
}

// New returns a client for the worker at baseURL. hc may be nil for Hermod's
// data client, which reaches private addresses: the worker runs beside Hermod.
func New(baseURL, token string, hc *http.Client) *Client {
	if hc == nil {
		hc = httpclient.DataClient
	}
	return &Client{URL: strings.TrimRight(baseURL, "/"), Token: token, TrainTimeout: DefaultTrainTimeout, http: hc}
}

// FromEnv returns the worker named by HERMOD_ML_WORKER_URL and
// HERMOD_ML_WORKER_TOKEN, or nil when no worker is configured.
func FromEnv() *Client {
	u := strings.TrimSpace(os.Getenv("HERMOD_ML_WORKER_URL"))
	if u == "" {
		return nil
	}
	c := New(u, os.Getenv("HERMOD_ML_WORKER_TOKEN"), nil)
	if d, err := time.ParseDuration(os.Getenv("HERMOD_ML_TRAIN_TIMEOUT")); err == nil && d > 0 {
		c.TrainTimeout = d
	}
	return c
}

// ServingURL is the Open Inference Protocol base URL of a vhost's models.
func (c *Client) ServingURL(vhost string) string {
	return c.URL + "/vhosts/" + url.PathEscape(vhost)
}

// Column is one column of a dataset; Type is number, string or bool.
type Column struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// DatasetInfo describes a dataset. Sample holds its first rows when one
// dataset is asked for, and is empty in a list.
type DatasetInfo struct {
	Name      string           `json:"name"`
	Rows      int              `json:"rows"`
	Columns   []Column         `json:"columns"`
	UpdatedAt time.Time        `json:"updated_at"`
	Sample    []map[string]any `json:"sample,omitempty"`
}

// TaskRecommendation is the task of a model that recommends items to people
// (ml/worker/hermod_ml/recommend.py) rather than predicting a column.
const TaskRecommendation = "recommendation"

// TrainSpec is one training: which dataset, what to predict from what, and how.
type TrainSpec struct {
	Dataset string `json:"dataset"`
	// Target is the column to predict; empty for a recommendation model.
	Target string `json:"target"`
	// Features are the columns the model reads; empty means every column but
	// Target.
	Features []string `json:"features,omitempty"`
	// Task is auto, classification, regression or recommendation.
	Task string `json:"task,omitempty"`
	// Recommend names a recommendation model's columns. The worker refuses
	// it for any other task, and Target, Features and Params for this one.
	Recommend *RecommendSpec `json:"recommend,omitempty"`
	// Algorithm is auto, random_forest, gradient_boosting, linear, xgboost,
	// extra_trees, hist_gradient_boosting, pytorch_mlp or keras_mlp; for a
	// recommendation model, auto, als or item_similarity.
	Algorithm string `json:"algorithm,omitempty"`
	// Preset is how hard auto tries: quick, balanced (the worker's default)
	// or best. Only an auto training takes one.
	Preset   string  `json:"preset,omitempty"`
	TestSize float64 `json:"test_size,omitempty"`
	Seed     *int    `json:"seed,omitempty"`
	// Device is cpu (the default) or gpu. Hermod routes a gpu training to its
	// GPU worker pool; the worker reads it as a hint for its own networks.
	Device string `json:"device,omitempty"`
	// Params tunes pytorch_mlp and keras_mlp; the worker refuses it for any
	// other algorithm.
	Params *TrainParams `json:"params,omitempty"`
}

// TrainParams are a deep-learning model's hyperparameters. A zero field is
// left out, so the worker uses its default; the worker also checks the
// bounds (at most 5 layers of 1024 units, 1000 epochs, a learning rate in
// (0, 1]).
type TrainParams struct {
	HiddenLayers []int   `json:"hidden_layers,omitempty"`
	Epochs       int     `json:"epochs,omitempty"`
	BatchSize    int     `json:"batch_size,omitempty"`
	LearningRate float64 `json:"learning_rate,omitempty"`
	// Patience is how many epochs without a better validation loss end
	// training early.
	Patience int `json:"patience,omitempty"`
}

// RecommendSpec is the columns a recommendation model learns from: who
// (User) picked what (Item), optionally how much they liked it (Strength: a
// rating, quantity or count) and when (Time, which makes the test hold back
// each person's latest picks).
type RecommendSpec struct {
	User     string `json:"user"`
	Item     string `json:"item"`
	Strength string `json:"strength,omitempty"`
	Time     string `json:"time,omitempty"`
}

// Catalog is how many people and items a recommendation model knows.
type Catalog struct {
	Users int `json:"users"`
	Items int `json:"items"`
}

// RecommendSample is one person of a recommendation model's test: what the
// model recommended, and what they really picked next.
type RecommendSample struct {
	User        string   `json:"user"`
	Recommended []string `json:"recommended"`
	Picked      []string `json:"picked"`
}

// Capabilities is what a worker can train. The slim image cannot train the
// deep-learning algorithms; Unavailable says why, per algorithm.
type Capabilities struct {
	Tasks       []string          `json:"tasks"`
	Algorithms  []string          `json:"algorithms"`
	Unavailable map[string]string `json:"unavailable"`
}

// Version is one trained, immutable model version and how it scored on the
// rows held back from training.
type Version struct {
	Model        string             `json:"model"`
	Version      string             `json:"version"`
	Task         string             `json:"task"`
	Algorithm    string             `json:"algorithm"`
	Dataset      string             `json:"dataset"`
	Target       string             `json:"target"`
	Features     []string           `json:"features"`
	FeatureTypes map[string]string  `json:"feature_types,omitempty"`
	Labels       []any              `json:"labels,omitempty"`
	Metrics      map[string]float64 `json:"metrics"`
	Rows         struct {
		Train int `json:"train"`
		Test  int `json:"test"`
	} `json:"rows"`
	// FeatureStats describe the rows the version was trained on, per
	// feature. A version trained before the worker recorded them has none.
	FeatureStats map[string]FeatureStats `json:"feature_stats,omitempty"`
	CreatedAt    time.Time               `json:"created_at"`
	// Fill is what a null numeric feature becomes: its training median.
	Fill map[string]float64 `json:"fill,omitempty"`
	// Script is the custom script that trained this version, and Log what
	// it printed; both are empty for a built-in algorithm.
	Script *ScriptRef `json:"script,omitempty"`
	Log    string     `json:"log,omitempty"`

	// A recommendation version's columns, catalogue, a sample of its test,
	// and what auto measured for each algorithm (NDCG@10 on a validation
	// split of the training rows).
	Recommend  *RecommendSpec     `json:"recommend,omitempty"`
	Catalog    *Catalog           `json:"catalog,omitempty"`
	Samples    []RecommendSample  `json:"samples,omitempty"`
	Candidates map[string]float64 `json:"candidates,omitempty"`
	// Preset and Leaderboard are an auto training's: how hard it tried, and
	// every candidate it scored, best first.
	Preset      string             `json:"preset,omitempty"`
	Leaderboard []LeaderboardEntry `json:"leaderboard,omitempty"`
	// Importance is each feature's permutation importance on the held-out
	// rows, normalised to sum to 1, heaviest first, at most 20.
	Importance []FeatureWeight `json:"importance,omitempty"`
	// Baseline holds the Metrics keys, scored by a model that always answers
	// the most common class or the mean.
	Baseline map[string]float64 `json:"baseline,omitempty"`
	// Warnings are notes on the data, in words.
	Warnings []string `json:"warnings,omitempty"`
}

// LeaderboardEntry is one algorithm auto scored by cross-validation on the
// training rows. Metric is balanced_accuracy for a classifier and r2 for a
// regression; Folds is 1 for a single validation split.
type LeaderboardEntry struct {
	Algorithm string  `json:"algorithm"`
	Score     float64 `json:"score"`
	Metric    string  `json:"metric"`
	Folds     int     `json:"folds"`
	Seconds   float64 `json:"seconds"`
	Chosen    bool    `json:"chosen"`
}

// FeatureWeight is one feature's share of what the model relies on.
type FeatureWeight struct {
	Feature string  `json:"feature"`
	Weight  float64 `json:"weight"`
}

// TrainCheck is the pre-flight answer for a training: what the worker makes
// of the dataset, without training anything.
type TrainCheck struct {
	// Task is classification or regression; TargetKind yes_no, category or
	// number. Classes is set for a classification only.
	Task       string         `json:"task"`
	TargetKind string         `json:"target_kind"`
	Classes    *int           `json:"classes,omitempty"`
	Rows       int            `json:"rows"`
	UsableRows int            `json:"usable_rows"`
	Columns    []ColumnCheck  `json:"columns"`
	Warnings   []CheckWarning `json:"warnings"`
	// OK is false when a training would be refused; Warnings say why.
	OK bool `json:"ok"`
}

// ColumnCheck is one column's role: use, id_like, constant, mostly_empty,
// leak_suspect or target, with the reason in words (empty for use).
type ColumnCheck struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Role string `json:"role"`
	Note string `json:"note"`
}

// CheckWarning is one pre-flight note. Code is few_rows, imbalanced,
// id_like, constant, mostly_empty, leak_suspect or many_classes.
type CheckWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Column  string `json:"column,omitempty"`
}

// The kinds of FeatureStats.
const (
	StatsNumeric     = "numeric"
	StatsCategorical = "categorical"
)

// FeatureStats is one feature of a training split, as the worker measured it
// (ml/worker/hermod_ml/stats.py, which documents the binning).
type FeatureStats struct {
	Kind string `json:"kind"`
	// Count is the rows measured; NullFraction the share with no usable value.
	Count        int     `json:"count"`
	NullFraction float64 `json:"null_fraction"`

	// Numeric: summary values, absent when every value was missing, and the
	// decile bins. Bin i holds Edges[i-1] < v <= Edges[i]; Fractions has one
	// more entry than Edges, each bin's share of the non-missing values.
	Mean      *float64  `json:"mean,omitempty"`
	Std       *float64  `json:"std,omitempty"`
	Min       *float64  `json:"min,omitempty"`
	Max       *float64  `json:"max,omitempty"`
	Edges     []float64 `json:"edges,omitempty"`
	Fractions []float64 `json:"fractions,omitempty"`

	// Categorical: the most frequent values, and the share of all the others.
	Top           []CategoryShare `json:"top,omitempty"`
	OtherFraction float64         `json:"other_fraction,omitempty"`
}

// CategoryShare is one value of a categorical feature and its share.
type CategoryShare struct {
	Value    string  `json:"value"`
	Fraction float64 `json:"fraction"`
}

// Ready reports whether the worker answers its readiness check.
func (c *Client) Ready(ctx context.Context) error {
	return c.call(ctx, http.MethodGet, "/v2/health/ready", "", nil, nil, callTimeout)
}

// Capabilities asks the worker what it can train. A worker older than this
// call answers ErrNotFound.
func (c *Client) Capabilities(ctx context.Context) (Capabilities, error) {
	var caps Capabilities
	err := c.call(ctx, http.MethodGet, "/v1/capabilities", "", nil, &caps, callTimeout)
	return caps, err
}

// AppendRows adds rows to a dataset, first emptying it when replace is set,
// and returns how many rows the dataset holds now.
func (c *Client) AppendRows(ctx context.Context, vhost, dataset string, rows []map[string]any, replace bool) (int, error) {
	p, err := datasetPath(vhost, dataset)
	if err != nil {
		return 0, err
	}
	body, err := json.Marshal(map[string]any{"rows": rows, "replace": replace})
	if err != nil {
		return 0, fmt.Errorf("encoding rows: %w", err)
	}
	var out struct {
		Rows int `json:"rows"`
	}
	if err := c.call(ctx, http.MethodPost, p+"/rows", "application/json", bytes.NewReader(body), &out, callTimeout); err != nil {
		return 0, err
	}
	return out.Rows, nil
}

// UploadFile replaces a dataset with a CSV or Excel (.xlsx) file.
func (c *Client) UploadFile(ctx context.Context, vhost, dataset, format string, body io.Reader) (DatasetInfo, error) {
	if format != "csv" && format != "xlsx" {
		return DatasetInfo{}, fmt.Errorf("a dataset file is csv or xlsx, not %q", format)
	}
	p, err := datasetPath(vhost, dataset)
	if err != nil {
		return DatasetInfo{}, err
	}
	var info DatasetInfo
	err = c.call(ctx, http.MethodPut, p+"/file?format="+format, "application/octet-stream", body, &info, c.TrainTimeout)
	return info, err
}

// Datasets lists a vhost's datasets.
func (c *Client) Datasets(ctx context.Context, vhost string) ([]DatasetInfo, error) {
	if err := checkName("vhost", vhost); err != nil {
		return nil, err
	}
	var out struct {
		Datasets []DatasetInfo `json:"datasets"`
	}
	err := c.call(ctx, http.MethodGet, "/v1/datasets/"+vhost, "", nil, &out, callTimeout)
	return out.Datasets, err
}

// Dataset describes one dataset, with a sample of its rows.
func (c *Client) Dataset(ctx context.Context, vhost, dataset string) (DatasetInfo, error) {
	p, err := datasetPath(vhost, dataset)
	if err != nil {
		return DatasetInfo{}, err
	}
	var info DatasetInfo
	err = c.call(ctx, http.MethodGet, p, "", nil, &info, callTimeout)
	return info, err
}

// DeleteDataset removes a dataset.
func (c *Client) DeleteDataset(ctx context.Context, vhost, dataset string) error {
	p, err := datasetPath(vhost, dataset)
	if err != nil {
		return err
	}
	return c.call(ctx, http.MethodDelete, p, "", nil, nil, callTimeout)
}

// Train trains a new version of the model and returns it. The call waits for
// the training to finish.
func (c *Client) Train(ctx context.Context, vhost, model string, spec TrainSpec) (Version, error) {
	p, err := modelPath(vhost, model)
	if err != nil {
		return Version{}, err
	}
	body, err := json.Marshal(spec)
	if err != nil {
		return Version{}, fmt.Errorf("encoding the training spec: %w", err)
	}
	var v Version
	err = c.call(ctx, http.MethodPost, p+"/train", "application/json", bytes.NewReader(body), &v, c.TrainTimeout)
	return v, err
}

// TrainCheck runs the pre-flight check of a training spec on a vhost's
// dataset. It trains nothing and takes no training slot.
func (c *Client) TrainCheck(ctx context.Context, vhost string, spec TrainSpec) (TrainCheck, error) {
	if err := checkName("vhost", vhost); err != nil {
		return TrainCheck{}, err
	}
	body, err := json.Marshal(spec)
	if err != nil {
		return TrainCheck{}, fmt.Errorf("encoding the training spec: %w", err)
	}
	var out TrainCheck
	err = c.call(ctx, http.MethodPost, "/v1/train-check/"+vhost, "application/json", bytes.NewReader(body), &out, callTimeout)
	return out, err
}

// Versions lists a model's versions, newest first.
func (c *Client) Versions(ctx context.Context, vhost, model string) ([]Version, error) {
	p, err := modelPath(vhost, model)
	if err != nil {
		return nil, err
	}
	var out struct {
		Versions []Version `json:"versions"`
	}
	err = c.call(ctx, http.MethodGet, p+"/versions", "", nil, &out, callTimeout)
	return out.Versions, err
}

// ModelFile returns the ONNX graph a version of a model serves, for scoring
// it in-process.
func (c *Client) ModelFile(ctx context.Context, vhost, model, version string) ([]byte, error) {
	p, err := modelPath(vhost, model)
	if err != nil {
		return nil, err
	}
	if err := checkName("version", version); err != nil {
		return nil, err
	}
	return c.do(ctx, http.MethodGet, p+"/versions/"+version+"/model.onnx", "", nil, callTimeout, maxModelFileBytes)
}

// RecommenderFile returns the artifact a recommendation version serves, for
// serving it in-process (pkg/ml/recommend).
func (c *Client) RecommenderFile(ctx context.Context, vhost, model, version string) ([]byte, error) {
	p, err := modelPath(vhost, model)
	if err != nil {
		return nil, err
	}
	if err := checkName("version", version); err != nil {
		return nil, err
	}
	return c.do(ctx, http.MethodGet, p+"/versions/"+version+"/model.rec", "", nil, callTimeout, maxModelFileBytes)
}

// DeleteModel removes every version of a model.
func (c *Client) DeleteModel(ctx context.Context, vhost, model string) error {
	p, err := modelPath(vhost, model)
	if err != nil {
		return err
	}
	return c.call(ctx, http.MethodDelete, p, "", nil, nil, callTimeout)
}

func datasetPath(vhost, dataset string) (string, error) {
	if err := checkName("vhost", vhost); err != nil {
		return "", err
	}
	if err := checkName("dataset", dataset); err != nil {
		return "", err
	}
	return "/v1/datasets/" + vhost + "/" + dataset, nil
}

func modelPath(vhost, model string) (string, error) {
	if err := checkName("vhost", vhost); err != nil {
		return "", err
	}
	if err := checkName("model", model); err != nil {
		return "", err
	}
	return "/v1/models/" + vhost + "/" + model, nil
}

// call sends one request and decodes the JSON reply into out, when out is
// not nil. A worker error keeps the worker's own message.
func (c *Client) call(ctx context.Context, method, path, contentType string, body io.Reader, out any, timeout time.Duration) error {
	raw, err := c.do(ctx, method, path, contentType, body, timeout, maxReplyBytes)
	if err != nil {
		return err
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("the ML worker's reply is not the expected JSON: %w", err)
	}
	return nil
}

// do sends one request and returns the reply's body, at most limit bytes.
func (c *Client) do(ctx context.Context, method, path, contentType string, body io.Reader, timeout time.Duration, limit int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, method, c.URL+path, body)
	if err != nil {
		return nil, httpclient.RedactURLError(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling the ML worker: %w", httpclient.RedactURLError(err))
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("reading the ML worker's reply: %w", err)
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("the ML worker's reply is larger than %d bytes", limit)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, workerError(resp.StatusCode, raw)
	}
	return raw, nil
}

func workerError(status int, raw []byte) error {
	var e struct {
		Error string `json:"error"`
	}
	msg := strings.TrimSpace(string(raw))
	if json.Unmarshal(raw, &e) == nil && e.Error != "" {
		msg = e.Error
	}
	if len(msg) > 512 {
		msg = msg[:512] + "…"
	}
	switch status {
	case http.StatusNotFound:
		return fmt.Errorf("%w: %s", ErrNotFound, msg)
	case http.StatusTooManyRequests:
		return fmt.Errorf("%w: %s", ErrBusy, msg)
	}
	return fmt.Errorf("the ML worker answered %d: %s", status, msg)
}
