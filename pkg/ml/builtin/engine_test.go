package builtin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/pkg/ml/worker"
)

func newEngine(t *testing.T) (*Engine, *worker.Client) {
	t.Helper()
	e, err := New(Config{Dir: t.TempDir(), Workers: 2})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	return e, e.Client()
}

// linearRows are y = 3a - 2b + 5 with a text column that does not matter.
func linearRows(n int) []map[string]any {
	rng := rand.New(rand.NewPCG(1, 2))
	rows := make([]map[string]any, n)
	for i := range rows {
		a, b := rng.Float64()*10, rng.Float64()*10
		rows[i] = map[string]any{"a": a, "b": b, "colour": []string{"red", "green", "blue"}[i%3], "y": 3*a - 2*b + 5}
	}
	return rows
}

// churnRows are a yes/no target that the plan and the tenure decide.
func churnRows(n int) []map[string]any {
	rng := rand.New(rand.NewPCG(3, 4))
	rows := make([]map[string]any, n)
	for i := range rows {
		tenure := float64(rng.IntN(60))
		plan := []string{"basic", "pro", "team"}[rng.IntN(3)]
		churn := "no"
		if plan == "basic" && tenure < 30 {
			churn = "yes"
		}
		rows[i] = map[string]any{"tenure": tenure, "plan": plan, "churn": churn}
	}
	return rows
}

func appendRows(t *testing.T, c *worker.Client, name string, rows []map[string]any) {
	t.Helper()
	if _, err := c.AppendRows(context.Background(), "v", name, rows, true); err != nil {
		t.Fatalf("AppendRows: %v", err)
	}
}

func seed(v int) *int { return &v }

func TestCapabilitiesListTheBuiltinAlgorithms(t *testing.T) {
	_, c := newEngine(t)
	if err := c.Ready(context.Background()); err != nil {
		t.Fatalf("Ready: %v", err)
	}
	caps, err := c.Capabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{"auto", "linear", "random_forest", "gradient_boosting", "mlp", "als", "item_similarity"} {
		if !slices.Contains(caps.Algorithms, a) {
			t.Errorf("algorithm %q is not offered", a)
		}
	}
	for _, a := range []string{"xgboost", "pytorch_mlp", "keras_mlp", "extra_trees", "hist_gradient_boosting"} {
		if caps.Unavailable[a] != NeedsWorker {
			t.Errorf("unavailable[%q] = %q, want %q", a, caps.Unavailable[a], NeedsWorker)
		}
	}
}

func TestDatasetsRoundTrip(t *testing.T) {
	_, c := newEngine(t)
	ctx := context.Background()
	if _, err := c.AppendRows(ctx, "v", "d", []map[string]any{{"a": 1.0, "s": "x"}}, false); err != nil {
		t.Fatal(err)
	}
	total, err := c.AppendRows(ctx, "v", "d", []map[string]any{{"a": 2.0, "s": "y", "flag": true}}, false)
	if err != nil || total != 2 {
		t.Fatalf("AppendRows = %d, %v; want 2 rows", total, err)
	}
	info, err := c.Dataset(ctx, "v", "d")
	if err != nil {
		t.Fatal(err)
	}
	if info.Rows != 2 || len(info.Columns) != 3 || len(info.Sample) != 2 {
		t.Errorf("dataset = %+v", info)
	}
	if info.Columns[2] != (worker.Column{Name: "flag", Type: "bool"}) {
		t.Errorf("columns = %v", info.Columns)
	}
	list, err := c.Datasets(ctx, "v")
	if err != nil || len(list) != 1 {
		t.Fatalf("Datasets = %v, %v", list, err)
	}
	up, err := c.UploadFile(ctx, "v", "d", "csv", strings.NewReader("a,b\n1,x\n2,y\n3,\n"))
	if err != nil || up.Rows != 3 || up.Columns[1].Type != "string" {
		t.Fatalf("UploadFile = %+v, %v", up, err)
	}
	if err := c.DeleteDataset(ctx, "v", "d"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Dataset(ctx, "v", "d"); !errors.Is(err, worker.ErrNotFound) {
		t.Errorf("deleted dataset: %v, want ErrNotFound", err)
	}
}

func TestTrainLinearRegressionKnowsTheAnswer(t *testing.T) {
	e, c := newEngine(t)
	ctx := context.Background()
	appendRows(t, c, "lin", linearRows(300))
	v, err := c.Train(ctx, "v", "m", worker.TrainSpec{Dataset: "lin", Target: "y", Algorithm: "linear"})
	if err != nil {
		t.Fatalf("Train: %v", err)
	}
	if v.Version != "1" || v.Model != "m" || v.Task != "regression" || v.Algorithm != "linear" {
		t.Errorf("version = %s %s %s %s", v.Model, v.Version, v.Task, v.Algorithm)
	}
	if v.Metrics["r2"] < 0.999 || v.Metrics["score"] != v.Metrics["r2"] || v.Metrics["rmse"] > 0.1 {
		t.Errorf("metrics = %v", v.Metrics)
	}
	if v.Rows.Train != 240 || v.Rows.Test != 60 {
		t.Errorf("rows = %+v, want 240/60", v.Rows)
	}
	if !slices.Equal(v.Features, []string{"a", "b", "colour"}) || v.FeatureTypes["colour"] != "string" || v.FeatureTypes["a"] != "number" {
		t.Errorf("features = %v %v", v.Features, v.FeatureTypes)
	}
	if _, ok := v.Fill["a"]; !ok || len(v.Fill) != 2 {
		t.Errorf("fill = %v", v.Fill)
	}
	if st := v.FeatureStats["a"]; st.Kind != worker.StatsNumeric || len(st.Edges) != 9 || len(st.Fractions) != 10 || st.Count != 240 {
		t.Errorf("feature_stats[a] = %+v", st)
	}
	if st := v.FeatureStats["colour"]; st.Kind != worker.StatsCategorical || len(st.Top) != 3 {
		t.Errorf("feature_stats[colour] = %+v", st)
	}
	if _, ok := v.Baseline["r2"]; !ok {
		t.Errorf("baseline = %v", v.Baseline)
	}
	// colour does not matter, so it weighs (next to) nothing.
	if len(v.Importance) < 2 || v.Importance[0].Feature != "a" || v.Importance[1].Feature != "b" ||
		(len(v.Importance) == 3 && v.Importance[2].Weight > 1e-6) {
		t.Errorf("importance = %v", v.Importance)
	}
	versions, err := c.Versions(ctx, "v", "m")
	if err != nil || len(versions) != 1 || versions[0].Metrics["r2"] != v.Metrics["r2"] {
		t.Fatalf("Versions = %v, %v", versions, err)
	}
	out, err := e.Predict(ctx, "v", "m", "1", []map[string]any{{"a": 1.0, "b": 2.0, "colour": "red"}, {"a": "4", "b": nil}})
	if err != nil {
		t.Fatalf("Predict: %v", err)
	}
	if got := out[0]["value"].(float64); math.Abs(got-4) > 0.05 {
		t.Errorf("prediction = %v, want 4", got)
	}
	// b missing takes its training median.
	if want := 3*4 - 2*v.Fill["b"] + 5; math.Abs(out[1]["value"].(float64)-want) > 0.05 {
		t.Errorf("prediction with a missing value = %v, want %v", out[1]["value"], want)
	}
}

func TestTrainClassifierAutoChoosesAndServesLabels(t *testing.T) {
	e, c := newEngine(t)
	ctx := context.Background()
	appendRows(t, c, "churn", churnRows(400))
	v, err := c.Train(ctx, "v", "churn", worker.TrainSpec{Dataset: "churn", Target: "churn", Preset: "quick"})
	if err != nil {
		t.Fatalf("Train: %v", err)
	}
	if v.Task != "classification" || v.Preset != "quick" || len(v.Leaderboard) != 2 {
		t.Fatalf("version = %s preset %q leaderboard %v", v.Task, v.Preset, v.Leaderboard)
	}
	if !v.Leaderboard[0].Chosen || v.Leaderboard[0].Algorithm != v.Algorithm || v.Leaderboard[0].Metric != "balanced_accuracy" ||
		v.Leaderboard[0].Folds != 1 {
		t.Errorf("leaderboard = %+v, algorithm %s", v.Leaderboard, v.Algorithm)
	}
	if !slices.Equal(v.Labels, []any{"no", "yes"}) {
		t.Errorf("labels = %v", v.Labels)
	}
	for _, k := range []string{"accuracy", "balanced_accuracy", "f1", "roc_auc", "score"} {
		if _, ok := v.Metrics[k]; !ok {
			t.Errorf("metric %s missing from %v", k, v.Metrics)
		}
	}
	if v.Metrics["accuracy"] < 0.9 {
		t.Errorf("accuracy = %v", v.Metrics["accuracy"])
	}
	out, err := e.Predict(ctx, "v", "churn", "1", []map[string]any{{"tenure": 3.0, "plan": "basic"}, {"tenure": 50, "plan": "team"}})
	if err != nil {
		t.Fatal(err)
	}
	if out[0]["label"] != "yes" || out[1]["label"] != "no" {
		t.Errorf("predictions = %v", out)
	}
	if p := out[0]["probability"].(float64); p <= 0.5 || p > 1 {
		t.Errorf("probability = %v", p)
	}
}

func TestTrainEveryBuiltinAlgorithmOnIntegerClasses(t *testing.T) {
	e, c := newEngine(t)
	ctx := context.Background()
	rows := make([]map[string]any, 200)
	for i := range rows {
		x := float64(i % 40)
		rows[i] = map[string]any{"x": x, "grade": float64(int(x) / 10)}
	}
	appendRows(t, c, "g", rows)
	for _, alg := range []string{"linear", "random_forest", "gradient_boosting", "mlp"} {
		v, err := c.Train(ctx, "v", alg, worker.TrainSpec{Dataset: "g", Target: "grade", Algorithm: alg})
		if err != nil {
			t.Fatalf("%s: %v", alg, err)
		}
		if !slices.Equal(v.Labels, []any{0.0, 1.0, 2.0, 3.0}) {
			t.Errorf("%s labels = %v", alg, v.Labels)
		}
		out, err := e.Predict(ctx, "v", alg, "1", []map[string]any{{"x": 35}})
		if err != nil {
			t.Fatal(err)
		}
		if alg != "linear" && out[0]["label"] != 3.0 {
			t.Errorf("%s predicted %v for x=35, want 3", alg, out[0]["label"])
		}
	}
}

func TestTrainIsDeterministicForASeed(t *testing.T) {
	e, c := newEngine(t)
	ctx := context.Background()
	appendRows(t, c, "churn", churnRows(300))
	var models [][]byte
	for i := range 3 {
		s := 7
		if i == 2 {
			s = 8
		}
		v, err := c.Train(ctx, "v", fmt.Sprint("m", i), worker.TrainSpec{Dataset: "churn", Target: "churn", Algorithm: "random_forest", Seed: &s})
		if err != nil {
			t.Fatal(err)
		}
		_, raw, err := e.models.file("v", v.Model, v.Version, modelFile)
		if err != nil {
			t.Fatal(err)
		}
		models = append(models, raw)
	}
	if !bytes.Equal(models[0], models[1]) {
		t.Error("one seed trained two different models")
	}
	if bytes.Equal(models[0], models[2]) {
		t.Error("two seeds trained the same model")
	}
}

func TestTrainRefusals(t *testing.T) {
	_, c := newEngine(t)
	ctx := context.Background()
	appendRows(t, c, "small", linearRows(5))
	appendRows(t, c, "lin", linearRows(50))
	for _, tc := range []struct {
		spec worker.TrainSpec
		want string
	}{
		{worker.TrainSpec{Dataset: "lin", Target: "y", Algorithm: "xgboost"}, NeedsWorker},
		{worker.TrainSpec{Dataset: "lin", Target: "y", Algorithm: "pytorch_mlp"}, NeedsWorker},
		{worker.TrainSpec{Dataset: "lin", Target: "y", Algorithm: "nope"}, "'algorithm' must be one of"},
		{worker.TrainSpec{Dataset: "small", Target: "y"}, "Training needs at least 10 rows with a target value; found 5."},
		{worker.TrainSpec{Dataset: "lin", Target: "zz"}, `Target column "zz" is not in dataset "lin".`},
		{worker.TrainSpec{Dataset: "lin", Target: "y", Features: []string{"y"}}, "is the target and cannot also be a feature"},
		{worker.TrainSpec{Dataset: "lin", Target: "y", Seed: seed(-1)}, "'seed' must be an integer"},
		{worker.TrainSpec{Dataset: "lin", Target: "y", Algorithm: "linear", Preset: "best"}, "'preset' applies only when 'algorithm' is auto."},
		{worker.TrainSpec{Dataset: "lin", Target: "y", Algorithm: "linear", Params: &worker.TrainParams{Epochs: 3}}, "'params' applies only to the algorithms mlp"},
		{worker.TrainSpec{Dataset: "lin", Target: "y", Algorithm: "mlp", Params: &worker.TrainParams{Epochs: 5000}}, "'params.epochs' must be an integer from 1 to 1000"},
		{worker.TrainSpec{Dataset: "lin", Target: "colour", Task: "regression"}, "Task regression needs a numeric target"},
		{worker.TrainSpec{Dataset: "lin", Target: "y", Task: "classification"}, "Classification needs a string, bool or integer target"},
		{worker.TrainSpec{Task: "recommendation", Scenario: "visits", Data: map[string]map[string]string{"visits": {"dataset": "lin"}}}, NeedsWorker},
	} {
		_, err := c.Train(ctx, "v", "m", tc.spec)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("Train(%+v) = %v, want an error with %q", tc.spec, err, tc.want)
		}
	}
	if _, err := c.Train(ctx, "v", "m", worker.TrainSpec{Dataset: "missing", Target: "y"}); !errors.Is(err, worker.ErrNotFound) {
		t.Errorf("a missing dataset: %v, want ErrNotFound", err)
	}
}

func TestTrainIsBusyWhenEverySlotIsTaken(t *testing.T) {
	e, c := newEngine(t)
	appendRows(t, c, "lin", linearRows(50))
	release, err := e.slot()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := c.Train(context.Background(), "v", "m", worker.TrainSpec{Dataset: "lin", Target: "y"}); !errors.Is(err, worker.ErrBusy) {
		t.Errorf("Train while busy: %v, want ErrBusy", err)
	}
}

func TestTrainStopsWhenCancelled(t *testing.T) {
	_, c := newEngine(t)
	appendRows(t, c, "lin", linearRows(50))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Train(ctx, "v", "m", worker.TrainSpec{Dataset: "lin", Target: "y"}); !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled training: %v, want context.Canceled", err)
	}
	if _, err := c.Versions(context.Background(), "v", "m"); !errors.Is(err, worker.ErrNotFound) {
		t.Errorf("a cancelled training saved a version: %v", err)
	}
}

func TestWorkerOnlyRoutesSayWhy(t *testing.T) {
	_, c := newEngine(t)
	ctx := context.Background()
	appendRows(t, c, "lin", linearRows(50))
	if _, err := c.Train(ctx, "v", "m", worker.TrainSpec{Dataset: "lin", Target: "y", Algorithm: "linear"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ModelFile(ctx, "v", "m", "1"); !errors.Is(err, worker.ErrNotFound) {
		t.Errorf("model.onnx: %v, want ErrNotFound", err)
	}
	if _, err := c.RecommenderFile(ctx, "v", "m", "1"); !errors.Is(err, worker.ErrNotFound) {
		t.Errorf("model.rec of a tabular model: %v, want ErrNotFound", err)
	}
	if err := c.DeleteModel(ctx, "v", "m"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Versions(ctx, "v", "m"); !errors.Is(err, worker.ErrNotFound) {
		t.Errorf("deleted model: %v", err)
	}
}

func TestTrainCheckGivesEveryColumnARole(t *testing.T) {
	_, c := newEngine(t)
	rows := make([]map[string]any, 120)
	for i := range rows {
		label := []string{"a", "b"}[i%2]
		rows[i] = map[string]any{
			"id":     fmt.Sprint("row-", i),
			"same":   "x",
			"empty":  nil,
			"answer": label + "!",
			"noise":  float64((i * 7) % 13),
			"label":  label,
		}
		if i%10 == 0 {
			rows[i]["empty"] = 1.0
		}
	}
	appendRows(t, c, "d", rows)
	chk, err := c.TrainCheck(context.Background(), "v", worker.TrainSpec{Dataset: "d", Target: "label"})
	if err != nil {
		t.Fatal(err)
	}
	if !chk.OK || chk.Task != "classification" || chk.TargetKind != "yes_no" || chk.Classes == nil || *chk.Classes != 2 ||
		chk.Rows != 120 || chk.UsableRows != 120 {
		t.Errorf("check = %+v", chk)
	}
	roles := map[string]string{}
	for _, col := range chk.Columns {
		roles[col.Name] = col.Role
	}
	want := map[string]string{"id": "id_like", "same": "constant", "empty": "mostly_empty", "answer": "leak_suspect", "noise": "use", "label": "target"}
	for name, role := range want {
		if roles[name] != role {
			t.Errorf("role of %s = %q, want %q", name, roles[name], role)
		}
	}
	// Rows sent as JSON objects name their keys in sorted order.
	if chk.Columns[0].Name != "answer" || chk.Columns[3].Name != "label" {
		t.Errorf("columns are not in the dataset's order: %v", chk.Columns)
	}
	codes := map[string]bool{}
	for _, w := range chk.Warnings {
		codes[w.Code] = true
	}
	for _, code := range []string{"id_like", "constant", "mostly_empty", "leak_suspect"} {
		if !codes[code] {
			t.Errorf("no %s warning in %v", code, chk.Warnings)
		}
	}
}
