// Package inference calls a model server and turns its answer back into rows.
//
// Hermod does not run models in its own process: it is built without cgo, and
// a model that runs out of memory must not take a CDC pipeline down with it.
// A model is served by something else — Hermod's own hermod-ml worker, KServe,
// Triton, Seldon MLServer or an MLflow scoring server — and this package is
// the one client every caller (the Predict node, the REST and gRPC serving
// endpoints) goes through.
//
// Two wire protocols cover those servers:
//
//   - BackendOIP, the Open Inference Protocol (KServe "V2"), spoken by KServe,
//     Triton, MLServer and hermod-ml: POST {url}/v2/models/{model}[/versions/{v}]/infer.
//   - BackendMLflow, the MLflow scoring server: POST {url}/invocations.
package inference

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gsoultan/hermod/pkg/infra/httpclient"
)

// Backend names the wire protocol a model server speaks.
type Backend string

const (
	// BackendOIP is the Open Inference Protocol (KServe V2).
	BackendOIP Backend = "oip"
	// BackendMLflow is the MLflow scoring server protocol.
	BackendMLflow Backend = "mlflow"
)

// Row is one record sent to a model, or one prediction read back.
type Row = map[string]any

// DefaultTimeout bounds a call whose Target names none.
const DefaultTimeout = 30 * time.Second

// maxResponseBytes bounds what a model server may send back. A prediction for
// a batch of rows is small; a reply larger than this is a misconfigured server.
const maxResponseBytes = 32 << 20

// Target says where a model is served and how to call it.
type Target struct {
	Backend Backend
	// URL is the server's base URL, without the protocol's path.
	URL string
	// Model and Version name the model on an OIP server. Version may be empty
	// for the server's default. MLflow serves one model per URL and ignores both.
	Model   string
	Version string
	// Token, when set, is sent as "Authorization: Bearer <Token>".
	Token string
	// InputName switches OIP to matrix mode: one FP32 tensor of this name,
	// shaped [rows, len(Features)], with the values in Features order. This is
	// what an ONNX model exported from scikit-learn, PyTorch or TensorFlow
	// usually takes. Empty means column mode: one tensor per field.
	InputName string
	Features  []string
	// Timeout bounds the whole call. Zero means DefaultTimeout.
	Timeout time.Duration
}

// Validate reports what is wrong with a target before anything is sent to it.
func (t Target) Validate() error {
	switch t.Backend {
	case BackendOIP, BackendMLflow:
	default:
		return fmt.Errorf("unknown model server protocol %q: use %q or %q", t.Backend, BackendOIP, BackendMLflow)
	}
	u, err := url.Parse(t.URL)
	if t.URL == "" || err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("the model server URL must be an http:// or https:// address")
	}
	if t.Backend == BackendOIP {
		if !validPathSegment(t.Model) {
			return errors.New("an Open Inference Protocol server needs the model's name, without slashes")
		}
		if t.Version != "" && !validPathSegment(t.Version) {
			return errors.New("the model version must not contain slashes")
		}
		if t.InputName != "" && len(t.Features) == 0 {
			return errors.New("a single input tensor needs the feature list, in the order the model expects")
		}
	}
	return nil
}

// validPathSegment keeps a name from walking out of the path it is put in.
func validPathSegment(s string) bool {
	return s != "" && s != "." && s != ".." && !strings.ContainsAny(s, "/\\?#%")
}

// Client calls model servers.
type Client struct {
	http *http.Client
}

// NewClient returns a client using hc, or Hermod's data client when hc is nil.
// The data client allows private addresses on purpose: a model server is
// usually inside the same network, like a database.
func NewClient(hc *http.Client) *Client {
	if hc == nil {
		hc = httpclient.DataClient
	}
	return &Client{http: hc}
}

// Predict sends rows to the model and returns one prediction per row, in order.
func (c *Client) Predict(ctx context.Context, t Target, rows []Row) ([]Row, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	if err := t.Validate(); err != nil {
		return nil, err
	}
	timeout := t.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	switch t.Backend {
	case BackendMLflow:
		return c.predictMLflow(ctx, t, rows)
	default:
		return c.predictOIP(ctx, t, rows)
	}
}

// post sends body as JSON and decodes the JSON reply into out.
func (c *Client) post(ctx context.Context, t Target, endpoint string, body, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encoding the request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return httpclient.RedactURLError(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if t.Token != "" {
		req.Header.Set("Authorization", "Bearer "+t.Token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("calling the model server: %w", httpclient.RedactURLError(err))
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("reading the model server's reply: %w", err)
	}
	if len(raw) > maxResponseBytes {
		return fmt.Errorf("the model server's reply is larger than %d bytes", maxResponseBytes)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("the model server answered %d: %s", resp.StatusCode, excerpt(raw))
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("the model server's reply is not the expected JSON: %w", err)
	}
	return nil
}

// excerpt keeps an error body short enough to log.
func excerpt(raw []byte) string {
	const limit = 512
	s := strings.TrimSpace(string(raw))
	if len(s) > limit {
		s = s[:limit] + "…"
	}
	return s
}
