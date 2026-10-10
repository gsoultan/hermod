package storage

import (
	"strings"
	"testing"
	"time"

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

func TestScoringIsTheWorkerOrInProcess(t *testing.T) {
	for _, ok := range []string{"", MLScoringWorker, MLScoringInProcess} {
		if err := ValidateMLScoring(ok); err != nil {
			t.Errorf("ValidateMLScoring(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"in-process", "WORKER", "gpu"} {
		if err := ValidateMLScoring(bad); err == nil {
			t.Errorf("ValidateMLScoring(%q) accepted it", bad)
		}
	}
}

func TestMonitoringIsOffUntilSetAndValidated(t *testing.T) {
	var off MLMonitoring
	if off.Logging() {
		t.Error("prediction logging is on by default")
	}
	if warn, alert := off.Thresholds(); warn != 0.1 || alert != 0.25 {
		t.Errorf("default thresholds = %v, %v; want 0.1, 0.25", warn, alert)
	}
	if got := off.Retention(); got != 7*24*time.Hour {
		t.Errorf("default retention = %v, want 7d", got)
	}

	on := MLMonitoring{LogSampleRate: 0.5, LogMaskFields: []string{"email", "card.number"}, LogMaskType: "partial",
		LogRetention: "30d", DriftWarn: 0.2, DriftAlert: 0.4}
	if err := on.Validate(); err != nil {
		t.Fatalf("a valid setting was refused: %v", err)
	}
	if !on.Logging() || on.Retention() != 30*24*time.Hour {
		t.Errorf("logging = %v, retention = %v", on.Logging(), on.Retention())
	}
	if warn, alert := on.Thresholds(); warn != 0.2 || alert != 0.4 {
		t.Errorf("thresholds = %v, %v", warn, alert)
	}

	for name, bad := range map[string]MLMonitoring{
		"sample rate above one":    {LogSampleRate: 1.5},
		"negative sample rate":     {LogSampleRate: -0.1},
		"unknown mask type":        {LogMaskType: "rot13"},
		"empty mask field":         {LogMaskFields: []string{""}},
		"retention not a duration": {LogRetention: "forever"},
		"retention under an hour":  {LogRetention: "10m"},
		"retention over a year":    {LogRetention: "400d"},
		"alert below warn":         {DriftWarn: 0.3, DriftAlert: 0.2},
		"negative threshold":       {DriftWarn: -1},
		"alert below default warn": {DriftAlert: 0.05},
		"too many mask fields":     {LogMaskFields: strings.Split(strings.Repeat("f,", 101), ",")[:101]},
	} {
		if err := bad.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
		m := MLModel{VHost: "v", Name: "churn", Backend: MLBackendWorker, Monitoring: bad}
		if err := ValidateMLModel(m); err == nil {
			t.Errorf("%s: a model with it was accepted", name)
		}
	}
}

func TestAnExternalModelStillNeedsItsServer(t *testing.T) {
	m := MLModel{VHost: "v", Name: "fraud", Backend: inference.BackendOIP, RemoteModel: "fraud"}
	if err := ValidateMLModel(m); err == nil {
		t.Error("an external model without a URL was accepted")
	}
}
