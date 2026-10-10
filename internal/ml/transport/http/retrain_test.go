package http

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/inference"
)

// modelAPIStore keeps retrain policies as the real stores do.
func (s *modelAPIStore) ListRetrainingMLModels(context.Context) ([]storage.MLModel, error) {
	return nil, nil
}
func (s *modelAPIStore) SetMLModelRetrain(_ context.Context, vhost, name string, p *storage.MLRetrainPolicy) error {
	m, ok := s.models[vhost+"/"+name]
	if !ok {
		return storage.ErrNotFound
	}
	m.Retrain = p
	s.models[vhost+"/"+name] = m
	return nil
}
func (s *modelAPIStore) SetMLModelRetrainStatus(_ context.Context, vhost, name string, st storage.MLRetrainStatus) error {
	m, ok := s.models[vhost+"/"+name]
	if !ok {
		return storage.ErrNotFound
	}
	m.RetrainStatus = &st
	s.models[vhost+"/"+name] = m
	return nil
}
func (s *modelAPIStore) ClaimMLModelTraining(_ context.Context, vhost, name, owner string, _ time.Duration) (bool, error) {
	if _, ok := s.models[vhost+"/"+name]; !ok {
		return false, storage.ErrNotFound
	}
	if s.claimedBy != "" && s.claimedBy != owner {
		return false, nil
	}
	s.claimedBy = owner
	return true, nil
}
func (s *modelAPIStore) ReleaseMLModelTraining(_ context.Context, _, _, owner string) error {
	if s.claimedBy == owner {
		s.claimedBy = ""
	}
	return nil
}

func TestARetrainPolicyIsSetAndClearedOverTheAPI(t *testing.T) {
	h, store, _ := newTrainingAPI(t)
	store.models["tenant-a/churn"] = storage.MLModel{VHost: "tenant-a", Name: "churn", Backend: storage.MLBackendWorker, RemoteVersion: "1"}
	store.models["tenant-a/fraud"] = storage.MLModel{VHost: "tenant-a", Name: "fraud", Backend: inference.BackendOIP, URL: "http://ml:8080", RemoteModel: "fraud"}
	vals := map[string]string{"vhost": "tenant-a", "name": "churn"}
	body := `{"schedule":"0 3 * * *","new_rows":500,"spec":{"dataset":"customers","target":"churned","algorithm":"xgboost"},"go_live":{"mode":"if","min":0.8}}`

	w := do(h.PutRetrain, editorA, http.MethodPut, "/x", strings.NewReader(body), vals)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"schedule":"0 3 * * *"`) {
		t.Fatalf("set = %d %s", w.Code, w.Body)
	}
	p := store.models["tenant-a/churn"].Retrain
	if p == nil || p.NewRows != 500 || p.Spec.Algorithm != "xgboost" || p.GoLive.Min == nil || p.UpdatedBy != "ada" {
		t.Errorf("stored = %+v", p)
	}
	if !hasAudit(store, `"retrain_schedule":"0 3 * * *"`) {
		t.Error("setting a retrain policy was not audited")
	}

	for _, tc := range []struct {
		name string
		user *storage.User
		vals map[string]string
		body string
		want int
	}{
		{"a viewer", viewerA, vals, body, http.StatusForbidden},
		{"another vhost's editor", editorB, vals, body, http.StatusForbidden},
		{"a model served elsewhere", editorA, map[string]string{"vhost": "tenant-a", "name": "fraud"}, body, http.StatusBadRequest},
		{"a model that does not exist", editorA, map[string]string{"vhost": "tenant-a", "name": "nope"}, body, http.StatusNotFound},
		{"no trigger", editorA, vals, `{"spec":{"dataset":"customers","target":"churned"}}`, http.StatusBadRequest},
		{"a bad schedule", editorA, vals, `{"schedule":"often","spec":{"dataset":"customers","target":"churned"}}`, http.StatusBadRequest},
		{"not JSON", editorA, vals, `{`, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if w := do(h.PutRetrain, tc.user, http.MethodPut, "/x", strings.NewReader(tc.body), tc.vals); w.Code != tc.want {
				t.Errorf("status = %d %s, want %d", w.Code, w.Body, tc.want)
			}
		})
	}

	if w := do(h.DeleteRetrain, viewerA, http.MethodDelete, "/x", nil, vals); w.Code != http.StatusForbidden {
		t.Errorf("a viewer cleared the policy: %d", w.Code)
	}
	if w := do(h.DeleteRetrain, editorA, http.MethodDelete, "/x", nil, vals); w.Code != http.StatusNoContent {
		t.Fatalf("clear = %d %s", w.Code, w.Body)
	}
	if store.models["tenant-a/churn"].Retrain != nil {
		t.Error("the policy stayed after it was cleared")
	}
}

func TestTrainingAModelAnotherTrainingHoldsIsAConflict(t *testing.T) {
	h, store, _ := newTrainingAPI(t)
	store.models["tenant-a/churn"] = storage.MLModel{VHost: "tenant-a", Name: "churn", Backend: storage.MLBackendWorker, RemoteVersion: "1"}
	store.claimedBy = "a-scheduled-retraining"
	w := do(h.TrainModel, editorA, http.MethodPost, "/x", strings.NewReader(`{"dataset":"customers","target":"churned"}`),
		map[string]string{"vhost": "tenant-a", "name": "churn"})
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "already being trained") {
		t.Errorf("train = %d %s, want 409", w.Code, w.Body)
	}
}
