// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package builtin

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// replicas are two engines over one store, as two Hermod servers over one
// metadata database are.
func replicas(t *testing.T) (*Engine, *Engine) {
	t.Helper()
	blobs, err := DirBlobs(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = CloseBlobs(blobs) })
	a, err := New(Config{Blobs: blobs, Workers: 2})
	if err != nil {
		t.Fatal(err)
	}
	b, err := New(Config{Blobs: blobs, Workers: 2})
	if err != nil {
		t.Fatal(err)
	}
	return a, b
}

func TestAReplicaTrainsOnDataAnotherFilledAndScoresWhatAnotherTrained(t *testing.T) {
	a, b := replicas(t)
	ctx := context.Background()
	appendRows(t, a.Client(), "lin", linearRows(200))
	if info, err := b.Client().Dataset(ctx, "v", "lin"); err != nil || info.Rows != 200 || info.Engine != worker.EngineBuiltin {
		t.Fatalf("the other replica sees the dataset as %+v, %v", info, err)
	}
	v, err := b.Client().Train(ctx, "v", "m", worker.TrainSpec{Dataset: "lin", Target: "y", Algorithm: "linear"})
	if err != nil {
		t.Fatalf("Train on the other replica: %v", err)
	}
	out, err := a.Predict(ctx, "v", "m", v.Version, []map[string]any{{"a": 1.0, "b": 2.0, "colour": "red"}})
	if err != nil || len(out) != 1 {
		t.Fatalf("Predict on the first replica = %v, %v", out, err)
	}
	if got := out[0]["value"].(float64); got < 3.9 || got > 4.1 {
		t.Errorf("predicted %v, want 4 (3*1 - 2*2 + 5)", got)
	}
	appendRows(t, b.Client(), "baskets", basketRows(60, 8))
	rec, err := a.Client().Train(ctx, "v", "rec", recommendSpec("item_similarity"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Client().RecommenderFile(ctx, "v", "rec", rec.Version); err != nil {
		t.Errorf("the other replica cannot serve the recommender: %v", err)
	}
}

func TestReplicasTrainingOneModelAtOnceGetTheirOwnVersions(t *testing.T) {
	a, b := replicas(t)
	ctx := context.Background()
	appendRows(t, a.Client(), "lin", linearRows(100))
	var wg sync.WaitGroup
	versions := make([]string, 4)
	errs := make([]error, 4)
	for i := range versions {
		e := a
		if i%2 == 1 {
			e = b
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			var v worker.Version
			for {
				v, errs[i] = e.Client().Train(ctx, "v", "m", worker.TrainSpec{Dataset: "lin", Target: "y", Algorithm: "linear"})
				if !errors.Is(errs[i], worker.ErrBusy) {
					break
				}
			}
			versions[i] = v.Version
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	slices.Sort(versions)
	if fmt.Sprint(versions) != "[b1 b2 b3 b4]" {
		t.Errorf("versions = %v, want b1 to b4, each once", versions)
	}
}

func TestADeletedModelsVersionIdsAreNotGivenAgain(t *testing.T) {
	_, c := newEngine(t)
	ctx := context.Background()
	appendRows(t, c, "lin", linearRows(100))
	spec := worker.TrainSpec{Dataset: "lin", Target: "y", Algorithm: "linear"}
	if _, err := c.Train(ctx, "v", "m", spec); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteModel(ctx, "v", "m"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Versions(ctx, "v", "m"); !errors.Is(err, worker.ErrNotFound) {
		t.Errorf("versions of a deleted model: %v, want ErrNotFound", err)
	}
	v, err := c.Train(ctx, "v", "m", spec)
	if err != nil || v.Version != "b2" {
		t.Errorf("the model trained again is version %q, %v; want b2, never b1 again", v.Version, err)
	}
}

func TestReplicasAppendingToOneDatasetLoseNoRows(t *testing.T) {
	a, b := replicas(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := range 8 {
		e := a
		if i%2 == 1 {
			e = b
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.Client().AppendRows(ctx, "v", "events", linearRows(25), false); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if info, err := a.Client().Dataset(ctx, "v", "events"); err != nil || info.Rows != 200 {
		t.Errorf("dataset = %+v, %v; want the 200 rows all 8 appends sent", info, err)
	}
}

// smallBlobs refuses a blob over max bytes, as a store with a limit does.
type smallBlobs struct {
	Blobs
	max int
}

func (s smallBlobs) Put(ctx context.Context, vhost, key string, data []byte) error {
	if len(data) > s.max {
		return fmt.Errorf("%w: %s", ErrBlobTooLarge, key)
	}
	return s.Blobs.Put(ctx, vhost, key, data)
}

func TestABlobTheStoreCannotKeepIsRefusedAsTooLarge(t *testing.T) {
	dir, err := DirBlobs(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = CloseBlobs(dir) })
	e, err := New(Config{Blobs: smallBlobs{Blobs: dir, max: 64}, Workers: 2})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Client().AppendRows(context.Background(), "v", "lin", linearRows(50), true)
	if err == nil || !strings.Contains(err.Error(), "413") || !strings.Contains(err.Error(), "too large") {
		t.Errorf("a part over the store's limit: %v; want a 413 that says it is too large", err)
	}
}

func TestReplacingADatasetLeavesNoOldParts(t *testing.T) {
	e, c := newEngine(t)
	ctx := context.Background()
	appendRows(t, c, "lin", linearRows(30))
	if _, err := c.AppendRows(ctx, "v", "lin", linearRows(10), false); err != nil {
		t.Fatal(err)
	}
	if _, err := c.AppendRows(ctx, "v", "lin", linearRows(5), true); err != nil {
		t.Fatal(err)
	}
	list, err := e.blobs.List(ctx, "v", "datasets/lin/", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 { // one part: its data and its info
		t.Errorf("after a replace the dataset keeps %d blobs, want 2: %+v", len(list), list)
	}
	if info, _ := c.Dataset(ctx, "v", "lin"); info.Rows != 5 {
		t.Errorf("rows = %d, want 5", info.Rows)
	}
}
