package sql

import (
	"errors"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/inference"
)

func mlModelStore(t *testing.T) storage.MLModelStore {
	t.Helper()
	withKey(t, "ml-model-test-key")
	var st storage.Storage = newRotationStorage(t)
	ms, ok := st.(storage.MLModelStore)
	if !ok {
		t.Fatal("the SQL store does not implement MLModelStore")
	}
	return ms
}

func churnModel(vhost string) storage.MLModel {
	return storage.MLModel{
		VHost: vhost, Name: "churn", Description: "who leaves",
		Backend: inference.BackendOIP, URL: "http://ml:8080", RemoteModel: "churn", RemoteVersion: "2",
		TokenSecret: "ML_TOKEN", InputName: "input", Features: []string{"age", "tenure"}, TimeoutMs: 1500,
		UpdatedBy: "ada",
	}
}

func TestMLModelRoundTripsAndStaysInItsVHost(t *testing.T) {
	st := mlModelStore(t)
	ctx := t.Context()
	if err := st.PutMLModel(ctx, churnModel("tenant-a")); err != nil {
		t.Fatalf("PutMLModel: %v", err)
	}

	got, err := st.GetMLModel(ctx, "tenant-a", "churn")
	if err != nil {
		t.Fatalf("GetMLModel: %v", err)
	}
	want := churnModel("tenant-a")
	if got.URL != want.URL || got.RemoteVersion != "2" || got.TokenSecret != "ML_TOKEN" ||
		got.InputName != "input" || len(got.Features) != 2 || got.Features[1] != "tenure" ||
		got.TimeoutMs != 1500 || got.Description != "who leaves" || got.UpdatedBy != "ada" {
		t.Errorf("round trip lost fields: %+v", got)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Errorf("no timestamps: %+v", got)
	}
	if got.Serving || got.ServingKeyHash != "" {
		t.Errorf("a new model is served before a key was made: %+v", got)
	}

	if _, err := st.GetMLModel(ctx, "tenant-b", "churn"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("another vhost read tenant-a's model: err = %v", err)
	}
}

func TestPutMLModelReplacesTheDefinitionButKeepsTheServingKey(t *testing.T) {
	st := mlModelStore(t)
	ctx := t.Context()
	m := churnModel("v")
	if err := st.PutMLModel(ctx, m); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMLModelServingKey(ctx, "v", "churn", "hash-1"); err != nil {
		t.Fatalf("SetMLModelServingKey: %v", err)
	}
	first, _ := st.GetMLModel(ctx, "v", "churn")

	m.URL = "http://ml-2:8080"
	if err := st.PutMLModel(ctx, m); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetMLModel(ctx, "v", "churn")
	if got.URL != "http://ml-2:8080" {
		t.Errorf("URL = %q, the update was lost", got.URL)
	}
	if got.ServingKeyHash != "hash-1" || !got.Serving {
		t.Errorf("an edit dropped the serving key: %+v", got)
	}
	if !got.CreatedAt.Equal(first.CreatedAt) {
		t.Errorf("an edit moved created_at from %v to %v", first.CreatedAt, got.CreatedAt)
	}

	if err := st.SetMLModelServingKey(ctx, "v", "churn", ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.GetMLModel(ctx, "v", "churn"); got.Serving {
		t.Error("serving stayed on after the key was cleared")
	}
	if err := st.SetMLModelServingKey(ctx, "v", "nope", "h"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("a key for a model that does not exist: err = %v", err)
	}
}

func TestMLModelListIsOrderedAndScopedAndDeletesWork(t *testing.T) {
	st := mlModelStore(t)
	ctx := t.Context()
	for _, name := range []string{"zeta", "alpha"} {
		m := churnModel("a")
		m.Name = name
		if err := st.PutMLModel(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.PutMLModel(ctx, churnModel("b")); err != nil {
		t.Fatal(err)
	}

	list, err := st.ListMLModels(ctx, "a")
	if err != nil {
		t.Fatalf("ListMLModels: %v", err)
	}
	if len(list) != 2 || list[0].Name != "alpha" || list[1].Name != "zeta" {
		t.Fatalf("list = %+v, want alpha then zeta and nothing of vhost b", list)
	}

	if err := st.DeleteMLModel(ctx, "a", "alpha"); err != nil {
		t.Fatalf("DeleteMLModel: %v", err)
	}
	if err := st.DeleteMLModel(ctx, "a", "alpha"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("deleting twice: err = %v, want ErrNotFound", err)
	}
	if err := st.DeleteMLModels(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if list, _ := st.ListMLModels(ctx, "a"); len(list) != 0 {
		t.Errorf("vhost a still has %d models", len(list))
	}
	if list, _ := st.ListMLModels(ctx, "b"); len(list) != 1 {
		t.Errorf("deleting vhost a's models touched vhost b: %+v", list)
	}
}

func TestPutMLModelRefusesAnInvalidModel(t *testing.T) {
	st := mlModelStore(t)
	m := churnModel("v")
	m.Name = "../etc"
	if err := st.PutMLModel(t.Context(), m); err == nil {
		t.Fatal("a model named ../etc was saved")
	}
}

// A deleted vhost must not leave its models for a later vhost of the same name.
func TestDeletingAVHostDeletesItsModels(t *testing.T) {
	withKey(t, "ml-model-test-key")
	s := newRotationStorage(t)
	if err := s.CreateVHost(t.Context(), storage.VHost{ID: "vh-1", Name: "tenant-a"}); err != nil {
		t.Fatalf("CreateVHost: %v", err)
	}
	for _, vhost := range []string{"tenant-a", "tenant-b"} {
		if err := s.PutMLModel(t.Context(), churnModel(vhost)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.DeleteVHost(t.Context(), "vh-1"); err != nil {
		t.Fatalf("DeleteVHost: %v", err)
	}
	if list, _ := s.ListMLModels(t.Context(), "tenant-a"); len(list) != 0 {
		t.Errorf("tenant-a was deleted and still holds %d models", len(list))
	}
	if list, _ := s.ListMLModels(t.Context(), "tenant-b"); len(list) != 1 {
		t.Errorf("deleting tenant-a left tenant-b with %d models, want 1", len(list))
	}
}

// The MCP flag and the feature types are part of the definition: they round
// trip, and a new model is not exposed until someone says so.
func TestMLModelKeepsItsMCPFlagAndFeatureTypes(t *testing.T) {
	st := mlModelStore(t)
	ctx := t.Context()
	m := churnModel("v")
	if err := st.PutMLModel(ctx, m); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.GetMLModel(ctx, "v", "churn"); got.MCPExposed {
		t.Fatal("a new model is exposed to MCP before anyone said so")
	}

	m.MCPExposed = true
	m.FeatureTypes = map[string]string{"age": "number", "tenure": "number"}
	if err := st.PutMLModel(ctx, m); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetMLModel(ctx, "v", "churn")
	if err != nil {
		t.Fatal(err)
	}
	if !got.MCPExposed || got.FeatureTypes["age"] != "number" || len(got.FeatureTypes) != 2 {
		t.Errorf("round trip lost the MCP flag or the feature types: %+v", got)
	}
	list, _ := st.ListMLModels(ctx, "v")
	if len(list) != 1 || !list[0].MCPExposed {
		t.Errorf("the list does not carry the MCP flag: %+v", list)
	}
}
