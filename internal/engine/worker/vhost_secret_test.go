package worker

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
)

// A worker in its own process has no database: its storage is the control
// plane's API. A workflow it runs that names secret("API_KEY") has to be able
// to read that vhost's secret through it, or the same workflow resolves the
// secret on the control plane and resolves nothing on a worker.
func TestWorkerStorageReadsAVHostSecretThroughTheAPI(t *testing.T) {
	var gotToken, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken, gotPath = r.Header.Get("X-Worker-Token"), r.URL.EscapedPath()
		if r.URL.Path == "/api/worker/vhosts/tenant a/secrets/API_KEY" {
			_, _ = w.Write([]byte(`{"value":"a-key"}`))
			return
		}
		http.Error(w, `{"error":"this vhost has no secret by that name"}`, http.StatusNotFound)
	}))
	defer srv.Close()

	store, ok := NewAPIStorage(NewWorkerAPIClient(srv.URL, "worker-token")).(storage.VHostSecretStore)
	if !ok {
		t.Fatal("the worker's API storage does not implement storage.VHostSecretStore")
	}

	got, err := store.GetVHostSecret(t.Context(), "tenant a", "API_KEY")
	if err != nil {
		t.Fatalf("GetVHostSecret: %v", err)
	}
	if got.Value != "a-key" || got.VHost != "tenant a" || got.Name != "API_KEY" {
		t.Errorf("got %+v, want tenant a/API_KEY = a-key", got)
	}
	if gotToken != "worker-token" {
		t.Errorf("X-Worker-Token = %q, want the worker's token", gotToken)
	}
	if gotPath != "/api/worker/vhosts/tenant%20a/secrets/API_KEY" {
		t.Errorf("request path = %q: the vhost must be escaped", gotPath)
	}

	if _, err := store.GetVHostSecret(t.Context(), "tenant a", "MISSING"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("a missing secret: err = %v, want ErrNotFound", err)
	}
}

// A worker reads secrets; it does not manage them.
func TestWorkerStorageDoesNotWriteVHostSecrets(t *testing.T) {
	store := NewAPIStorage(NewWorkerAPIClient("http://127.0.0.1:1", "")).(storage.VHostSecretStore)
	if err := store.PutVHostSecret(t.Context(), storage.VHostSecret{VHost: "v", Name: "N", Value: "x"}); !errors.Is(err, storage.ErrVHostSecretsUnsupported) {
		t.Errorf("PutVHostSecret: err = %v, want ErrVHostSecretsUnsupported", err)
	}
	if err := store.DeleteVHostSecret(t.Context(), "v", "N"); !errors.Is(err, storage.ErrVHostSecretsUnsupported) {
		t.Errorf("DeleteVHostSecret: err = %v, want ErrVHostSecretsUnsupported", err)
	}
}
