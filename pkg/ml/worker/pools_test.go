package worker

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestPoolFromEnvReadsItsOwnURLAndToken(t *testing.T) {
	t.Setenv("HERMOD_ML_CUSTOM_WORKER_URL", "")
	if c := PoolFromEnv("HERMOD_ML_CUSTOM_WORKER"); c != nil {
		t.Fatalf("PoolFromEnv() = %+v, want nil when the pool is not configured", c)
	}
	t.Setenv("HERMOD_ML_CUSTOM_WORKER_URL", "http://ml-custom:8090/")
	t.Setenv("HERMOD_ML_CUSTOM_WORKER_TOKEN", "custom-tok")
	t.Setenv("HERMOD_ML_WORKER_TOKEN", "main-tok")
	t.Setenv("HERMOD_ML_TRAIN_TIMEOUT", "45m")
	c := PoolFromEnv("HERMOD_ML_CUSTOM_WORKER")
	if c == nil || c.URL != "http://ml-custom:8090" || c.Token != "custom-tok" {
		t.Fatalf("PoolFromEnv() = %+v, want the pool's own URL and token", c)
	}
	if c.TrainTimeout.Minutes() != 45 {
		t.Errorf("TrainTimeout = %v, want the shared HERMOD_ML_TRAIN_TIMEOUT", c.TrainTimeout)
	}
}

func TestTrainCustomSendsTheSpecAndTheScript(t *testing.T) {
	f := &fakeWorker{reply: `{"model":"job-1","version":"1","algorithm":"custom:trees","script":{"name":"trees","sha256":"ab"},"log":"hello","metrics":{"score":0.8}}`}
	c := New(f.server(t).URL, "tok", nil)

	v, err := c.TrainCustom(t.Context(), "v", "job-1",
		TrainSpec{Dataset: "job-1", Target: "churned", Algorithm: "custom:trees"},
		Script{Name: "trees", SHA256: "ab", Source: "def train(df, spec): ..."})
	if err != nil {
		t.Fatalf("TrainCustom: %v", err)
	}
	if f.method != http.MethodPost || f.path != "/v1/models/v/job-1/train-custom" || f.auth != "Bearer tok" {
		t.Errorf("called %s %s with %q", f.method, f.path, f.auth)
	}
	var sent map[string]any
	if err := json.Unmarshal(f.body, &sent); err != nil {
		t.Fatal(err)
	}
	script, _ := sent["script"].(map[string]any)
	if sent["dataset"] != "job-1" || sent["algorithm"] != "custom:trees" || script["name"] != "trees" ||
		script["sha256"] != "ab" || script["source"] != "def train(df, spec): ..." {
		t.Errorf("body = %s", f.body)
	}
	if v.Script == nil || v.Script.Name != "trees" || v.Log != "hello" {
		t.Errorf("version = %+v", v)
	}
}

func TestDatasetExportAndImportStreamTheBytes(t *testing.T) {
	f := &fakeWorker{reply: "PAR1...PAR1"}
	c := New(f.server(t).URL, "", nil)

	rc, err := c.ExportDataset(t.Context(), "v", "orders")
	if err != nil {
		t.Fatalf("ExportDataset: %v", err)
	}
	got, _ := io.ReadAll(rc)
	_ = rc.Close()
	if string(got) != "PAR1...PAR1" || f.method != http.MethodGet || f.path != "/v1/datasets/v/orders/export" {
		t.Errorf("export read %q from %s %s", got, f.method, f.path)
	}

	f.reply = `{"name":"job-1","rows":3,"columns":[],"updated_at":"2026-10-10T00:00:00Z"}`
	info, err := c.ImportDataset(t.Context(), "v", "job-1", strings.NewReader("PAR1 bytes"))
	if err != nil {
		t.Fatalf("ImportDataset: %v", err)
	}
	if f.method != http.MethodPut || f.path != "/v1/datasets/v/job-1/import" || string(f.body) != "PAR1 bytes" || info.Rows != 3 {
		t.Errorf("import %s %s body %q info %+v", f.method, f.path, f.body, info)
	}
}

func TestVersionExportAndImport(t *testing.T) {
	f := &fakeWorker{reply: `{"meta":{},"onnx":"AAAA"}`}
	c := New(f.server(t).URL, "", nil)

	rc, err := c.ExportVersion(t.Context(), "v", "job-1", "1")
	if err != nil {
		t.Fatalf("ExportVersion: %v", err)
	}
	got, _ := io.ReadAll(rc)
	_ = rc.Close()
	if string(got) != `{"meta":{},"onnx":"AAAA"}` || f.path != "/v1/models/v/job-1/versions/1/export" {
		t.Errorf("export read %q from %s", got, f.path)
	}

	f.reply = `{"model":"churn","version":"4","dataset":"orders"}`
	v, err := c.ImportVersion(t.Context(), "v", "churn", "orders", strings.NewReader(`{"meta":{},"onnx":"AAAA"}`))
	if err != nil {
		t.Fatalf("ImportVersion: %v", err)
	}
	if f.method != http.MethodPost || f.path != "/v1/models/v/churn/import" || f.query != "dataset=orders" ||
		f.contentType != "application/json" || v.Version != "4" {
		t.Errorf("import %s %s?%s (%s) -> %+v", f.method, f.path, f.query, f.contentType, v)
	}
}

func TestExportErrorsKeepTheWorkersMessage(t *testing.T) {
	f := &fakeWorker{status: http.StatusNotFound, reply: `{"error":"Dataset 'nope' does not exist"}`}
	c := New(f.server(t).URL, "", nil)
	if _, err := c.ExportDataset(t.Context(), "v", "nope"); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("err = %v", err)
	}
	if _, err := c.ExportVersion(t.Context(), "v", "m", "../1"); err == nil {
		t.Error("a bad version was sent")
	}
}
