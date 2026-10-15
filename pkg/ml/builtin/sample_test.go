package builtin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/pkg/ml/worker"
)

func TestSampleSpreadsItsRowsOverTheWholeDataset(t *testing.T) {
	_, c := newEngine(t)
	ctx := context.Background()
	rows := make([]map[string]any, 100)
	for i := range rows {
		rows[i] = map[string]any{"n": float64(i), "s": fmt.Sprint("r", i), "ok": i%2 == 0}
	}
	// Two parts, so the sample reads across them.
	if _, err := c.AppendRows(ctx, "v", "d", rows[:60], true); err != nil {
		t.Fatal(err)
	}
	if _, err := c.AppendRows(ctx, "v", "d", rows[60:], false); err != nil {
		t.Fatal(err)
	}
	s, err := c.DatasetSample(ctx, "v", "d", 10)
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "d" || s.Rows != 100 || s.SampleRows != 10 || len(s.Columns) != 3 {
		t.Fatalf("sample = %+v", s)
	}
	n := s.Values["n"]
	if len(n) != 10 || n[0] != 0.0 || n[9] != 90.0 {
		t.Errorf("n = %v, want every tenth row from the first", n)
	}
	if s.Values["s"][5] != "r50" || s.Values["ok"][1] != true {
		t.Errorf("values = %v", s.Values)
	}

	all, err := c.DatasetSample(ctx, "v", "d", 1000)
	if err != nil || all.SampleRows != 100 {
		t.Errorf("asking for more rows than there are: %+v, %v", all.SampleRows, err)
	}
}

func TestSampleIsBounded(t *testing.T) {
	_, c := newEngine(t)
	ctx := context.Background()
	long := strings.Repeat("é", worker.SampleMaxText+50)
	if _, err := c.AppendRows(ctx, "v", "d", []map[string]any{{"note": long, "missing": nil}}, true); err != nil {
		t.Fatal(err)
	}
	s, err := c.DatasetSample(ctx, "v", "d", worker.SampleMaxRows*10)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Values["note"][0].(string); got != strings.Repeat("é", worker.SampleMaxText) {
		t.Errorf("a long text is %d characters, want it cut to %d", len([]rune(got)), worker.SampleMaxText)
	}
	if s.Values["missing"][0] != nil {
		t.Errorf("a missing value is %v, want nil", s.Values["missing"][0])
	}
	if _, err := c.DatasetSample(ctx, "v", "nope", 10); !errors.Is(err, worker.ErrNotFound) {
		t.Errorf("a missing dataset: %v, want ErrNotFound", err)
	}
}

func TestSampleRowsOfAWideDatasetAreFewer(t *testing.T) {
	if got := sampleRowsFor(worker.SampleMaxRows, 10); got != worker.SampleMaxRows {
		t.Errorf("10 columns: %d rows", got)
	}
	if got := sampleRowsFor(worker.SampleMaxRows, 100); got != worker.SampleMaxCells/100 {
		t.Errorf("100 columns: %d rows, want %d", got, worker.SampleMaxCells/100)
	}
	if got := sampleRowsFor(5, 0); got != 5 {
		t.Errorf("no columns: %d rows", got)
	}
}

func TestSampleOfADatasetOnlyTheWorkerHasComesFromIt(t *testing.T) {
	e, f, c := routed(t)
	f.datasets["legacy"] = linearRows(7)
	appendRows(t, e.Client(), "lin", linearRows(20))

	s, err := c.DatasetSample(context.Background(), "v", "legacy", 100)
	if err != nil || s.Rows != 7 {
		t.Fatalf("legacy sample = %+v, %v", s, err)
	}
	mine, err := c.DatasetSample(context.Background(), "v", "lin", 100)
	if err != nil || mine.Rows != 20 {
		t.Fatalf("the engine's sample = %+v, %v", mine, err)
	}
	if f.called("GET /v1/datasets/v/lin/sample") != 0 {
		t.Error("the engine's own dataset was sampled on the worker")
	}
}
