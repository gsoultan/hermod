package storage

import (
	"strconv"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/pkg/ml/inference"
)

func TestAWorkerModelNeedsNoURLAndTakesNone(t *testing.T) {
	m := MLModel{VHost: "v", Name: "churn", Backend: MLBackendWorker}
	if err := ValidateMLModel(m); err != nil {
		t.Fatalf("a trained model with nothing live yet was refused: %v", err)
	}
	m.RemoteVersion = "3"
	if err := ValidateMLModel(m); err != nil {
		t.Fatalf("a trained model with version 3 live was refused: %v", err)
	}

	m.URL = "http://elsewhere:8080"
	if err := ValidateMLModel(m); err == nil || !strings.Contains(err.Error(), "URL") {
		t.Errorf("a worker model took a URL of its own: err = %v", err)
	}
	m.URL, m.RemoteVersion = "", "../1"
	if err := ValidateMLModel(m); err == nil {
		t.Error("a version holding a path was accepted")
	}
	m.RemoteVersion, m.InputName = "1", "input"
	if err := ValidateMLModel(m); err == nil {
		t.Error("a worker model took a matrix input name; its features are its inputs")
	}
}

func TestAnExternalModelStillNeedsItsServer(t *testing.T) {
	m := MLModel{VHost: "v", Name: "fraud", Backend: inference.BackendOIP, RemoteModel: "fraud"}
	if err := ValidateMLModel(m); err == nil {
		t.Error("an external model without a URL was accepted")
	}
}

func TestFeatureTypesAreBounded(t *testing.T) {
	m := MLModel{VHost: "v", Name: "churn", Backend: MLBackendWorker, FeatureTypes: map[string]string{"age": "number"}}
	if err := ValidateMLModel(m); err != nil {
		t.Fatalf("a typed feature was refused: %v", err)
	}
	m.FeatureTypes = map[string]string{"age": strings.Repeat("t", 65)}
	if err := ValidateMLModel(m); err == nil {
		t.Error("an implausibly long feature type was accepted")
	}
	m.FeatureTypes = make(map[string]string, maxMLModelFeatures+1)
	for i := 0; i <= maxMLModelFeatures; i++ {
		m.FeatureTypes["f"+strconv.Itoa(i)] = "number"
	}
	if err := ValidateMLModel(m); err == nil {
		t.Error("more feature types than a model may declare features were accepted")
	}
}
