package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gsoultan/hermod/pkg/infra/httpclient"
)

// A training can run on a separate worker pool: one set aside for custom
// training scripts, or one on GPU nodes. A pool keeps nothing between
// trainings. The dataset is copied to it, the model is trained there, and the
// version is copied back to the worker that serves it; these are the calls
// that move them. The bytes are streamed from one worker to the other and
// never read here.

// PoolFromEnv returns the worker named by PREFIX_URL and PREFIX_TOKEN (for
// example HERMOD_ML_CUSTOM_WORKER_URL), or nil when PREFIX_URL is empty. The
// training timeout is HERMOD_ML_TRAIN_TIMEOUT, as for the main worker.
func PoolFromEnv(prefix string) *Client {
	u := strings.TrimSpace(os.Getenv(prefix + "_URL"))
	if u == "" {
		return nil
	}
	c := New(u, os.Getenv(prefix+"_TOKEN"), nil)
	if d, err := time.ParseDuration(os.Getenv("HERMOD_ML_TRAIN_TIMEOUT")); err == nil && d > 0 {
		c.TrainTimeout = d
	}
	return c
}

// Script is a custom training script as a worker runs it. SHA256 is the hex
// digest of Source; the worker refuses a script whose source does not match.
type Script struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Source string `json:"source"`
}

// ScriptRef names the script, and the exact version of it, that trained a
// model version.
type ScriptRef struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

// TrainCustom trains a new version of the model with a custom script. Only a
// worker started with HERMOD_ML_CUSTOM_SCRIPTS=true runs one.
func (c *Client) TrainCustom(ctx context.Context, vhost, model string, spec TrainSpec, script Script) (Version, error) {
	p, err := modelPath(vhost, model)
	if err != nil {
		return Version{}, err
	}
	body, err := json.Marshal(struct {
		TrainSpec
		Script Script `json:"script"`
	}{spec, script})
	if err != nil {
		return Version{}, fmt.Errorf("encoding the training spec: %w", err)
	}
	var v Version
	err = c.call(ctx, http.MethodPost, p+"/train-custom", "application/json", bytes.NewReader(body), &v, c.TrainTimeout)
	return v, err
}

// ExportDataset streams the whole dataset as one parquet file. The caller
// closes the reader.
func (c *Client) ExportDataset(ctx context.Context, vhost, dataset string) (io.ReadCloser, error) {
	p, err := datasetPath(vhost, dataset)
	if err != nil {
		return nil, err
	}
	return c.stream(ctx, p+"/export")
}

// ImportDataset replaces a dataset with a parquet file from ExportDataset.
func (c *Client) ImportDataset(ctx context.Context, vhost, dataset string, body io.Reader) (DatasetInfo, error) {
	p, err := datasetPath(vhost, dataset)
	if err != nil {
		return DatasetInfo{}, err
	}
	var info DatasetInfo
	err = c.call(ctx, http.MethodPut, p+"/import", "application/vnd.apache.parquet", body, &info, c.TrainTimeout)
	return info, err
}

// ExportVersion streams one version, its meta and its ONNX file, as JSON. The
// caller closes the reader.
func (c *Client) ExportVersion(ctx context.Context, vhost, model, version string) (io.ReadCloser, error) {
	p, err := modelPath(vhost, model)
	if err != nil {
		return nil, err
	}
	if err := checkName("version", version); err != nil {
		return nil, err
	}
	return c.stream(ctx, p+"/versions/"+version+"/export")
}

// ImportVersion adds a version from ExportVersion to the model, as its next
// version. dataset, when set, replaces the dataset name the version records:
// a pool trains on a copy under a name of its own. The worker checks the
// version as if it had never seen it, because it has not.
func (c *Client) ImportVersion(ctx context.Context, vhost, model, dataset string, body io.Reader) (Version, error) {
	p, err := modelPath(vhost, model)
	if err != nil {
		return Version{}, err
	}
	path := p + "/import"
	if dataset != "" {
		if err := checkName("dataset", dataset); err != nil {
			return Version{}, err
		}
		path += "?dataset=" + url.QueryEscape(dataset)
	}
	var v Version
	err = c.call(ctx, http.MethodPost, path, "application/json", body, &v, c.TrainTimeout)
	return v, err
}

// stream sends a GET and hands back the body of a 2xx reply. Its context ends
// when the body is closed.
func (c *Client) stream(ctx context.Context, path string) (io.ReadCloser, error) {
	ctx, cancel := context.WithTimeout(ctx, c.TrainTimeout)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL+path, nil)
	if err != nil {
		cancel()
		return nil, httpclient.RedactURLError(err)
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("calling the ML worker: %w", httpclient.RedactURLError(err))
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxReplyBytes))
		_ = resp.Body.Close()
		cancel()
		return nil, workerError(resp.StatusCode, raw)
	}
	return &cancelOnClose{ReadCloser: resp.Body, cancel: cancel}, nil
}

type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (r *cancelOnClose) Close() error {
	err := r.ReadCloser.Close()
	r.cancel()
	return err
}
