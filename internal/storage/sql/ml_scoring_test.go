package sql

import (
	"errors"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
)

func scoringStore(t *testing.T) (storage.MLModelStore, storage.MLScoringStore) {
	t.Helper()
	st := mlModelStore(t)
	ss, ok := st.(storage.MLScoringStore)
	if !ok {
		t.Fatal("the SQL store does not implement MLScoringStore")
	}
	return st, ss
}

// TestScoringIsKeptApartFromTheDefinition: how a model is scored is its own
// column, so training a new version — which saves the definition it read
// before a training that can take minutes — does not undo a change made
// meanwhile.
func TestScoringIsKeptApartFromTheDefinition(t *testing.T) {
	st, ss := scoringStore(t)
	ctx := t.Context()
	if err := st.PutMLModel(ctx, trainedModel("tenant-a", "churn")); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetMLModel(ctx, "tenant-a", "churn")
	if err != nil || got.Scoring != "" {
		t.Fatalf("a new model scores %q (%v), want the default", got.Scoring, err)
	}

	if err := ss.SetMLModelScoring(ctx, "tenant-a", "churn", storage.MLScoringInProcess); err != nil {
		t.Fatalf("SetMLModelScoring: %v", err)
	}
	m := trainedModel("tenant-a", "churn")
	m.RemoteVersion = "2"
	if err := st.PutMLModel(ctx, m); err != nil {
		t.Fatal(err)
	}
	got, _ = st.GetMLModel(ctx, "tenant-a", "churn")
	if got.Scoring != storage.MLScoringInProcess || got.RemoteVersion != "2" {
		t.Errorf("after an edit: scoring %q, version %q", got.Scoring, got.RemoteVersion)
	}
	list, _ := st.ListMLModels(ctx, "tenant-a")
	if len(list) != 1 || list[0].Scoring != storage.MLScoringInProcess {
		t.Errorf("listed = %+v", list)
	}

	if err := ss.SetMLModelScoring(ctx, "tenant-a", "churn", storage.MLScoringWorker); err != nil {
		t.Fatal(err)
	}
	if got, _ = st.GetMLModel(ctx, "tenant-a", "churn"); got.Scoring != storage.MLScoringWorker {
		t.Errorf("scoring = %q after going back to the worker", got.Scoring)
	}
}

func TestSetMLModelScoringRefusesWhatIsNotThere(t *testing.T) {
	st, ss := scoringStore(t)
	ctx := t.Context()
	if err := ss.SetMLModelScoring(ctx, "tenant-a", "nope", storage.MLScoringInProcess); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("a model that does not exist: %v, want ErrNotFound", err)
	}
	if err := st.PutMLModel(ctx, trainedModel("tenant-a", "churn")); err != nil {
		t.Fatal(err)
	}
	if err := ss.SetMLModelScoring(ctx, "tenant-a", "churn", "gpu"); err == nil {
		t.Error("an unknown scoring mode was stored")
	}
}
