// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

// Command gomlp measures the built-in engine's neural network (learn's mlp)
// on a generated tabular dataset: peak memory while it trains, CPU time,
// wall time, the saved model's size, scoring latency and throughput, and
// its quality against always answering the most common class.
//
//	go run ./ml/bench/gomlp -rows 200000
//	go run ./ml/bench/gomlp -rows 1000000 -epochs 10
//
// It prints one JSON object. Peak memory is the process's resident set
// (VmHWM, Linux only), reset after the data is generated, so it is what
// training holds on top of the data it is given. Run one configuration per
// process.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gsoultan/hermod/pkg/ml/builtin/learn"
)

func main() {
	rows := flag.Int("rows", 200_000, "rows to generate")
	epochs := flag.Int("epochs", 30, "most epochs to train")
	seed := flag.Uint64("seed", 1, "seed of the data and the training")
	latencyRows := flag.Int("latency-rows", 5000, "rows scored one at a time for the latency percentiles")
	cpuProfile := flag.String("cpuprofile", "", "write a CPU profile of the training to this file")
	opts := addOptions()
	flag.Parse()

	x, y := dataset(*rows, *seed)
	train, test := learn.Split(x.N, y, 0.2, *seed)
	xTrain, xTest := x.Take(train), x.Take(test)
	yTrain, yTest := pick(y, train), pick(y, test)
	baseline := majority(yTrainOf(y, train), yTest)
	x = nil

	params, err := learn.MLPParams{Epochs: *epochs}.WithDefaults()
	if err != nil {
		fail(err)
	}
	spec := learn.Spec{Task: learn.TaskClassification, Algorithm: learn.AlgorithmMLP, Classes: 2, Seed: *seed, MLP: params}
	opts.apply(&spec)

	out := map[string]any{"rows": *rows, "train_rows": len(train), "test_rows": len(test), "columns": 40,
		"epochs_max": *epochs, "gomaxprocs": runtime.GOMAXPROCS(0)}
	// Nothing reads the training rows after fitAndEncode, so they are
	// garbage before the scoring is measured.
	raw := fitAndEncode(spec, xTrain, yTrain, *cpuProfile, opts, out)
	m, err := learn.Decode(raw)
	if err != nil {
		fail(err)
	}
	measureScoring(m, xTest, yTest, *latencyRows, out)
	out["baseline_accuracy"] = round4(baseline)

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fail(err)
	}
}

// fitAndEncode trains the network on the training rows, recording its time,
// CPU, peak resident memory and allocations in out, and returns the saved
// model.
func fitAndEncode(spec learn.Spec, xTrain *learn.Frame, yTrain []float64, cpuProfile string, opts *options, out map[string]any) []byte {
	// Hand the generator's garbage back, so the resident set is the data.
	debug.FreeOSMemory()
	out["rss_before_train_mb"] = mb(status("VmRSS"))
	resetPeak()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	alloc0 := ms.TotalAlloc
	if cpuProfile != "" {
		f, err := os.Create(cpuProfile) //nolint:gosec // G304: the path the person running the benchmark gave.
		if err != nil {
			fail(err)
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			fail(err)
		}
	}
	cpu0, wall0 := cpuSeconds(), time.Now()
	m, err := learn.Fit(context.Background(), spec, xTrain, yTrain, nil)
	if err != nil {
		fail(err)
	}
	pprof.StopCPUProfile()
	out["train_wall_s"] = round(time.Since(wall0).Seconds())
	out["train_cpu_s"] = round(cpuSeconds() - cpu0)
	out["train_peak_rss_mb"] = mb(status("VmHWM"))
	runtime.ReadMemStats(&ms)
	out["train_alloc_mb"] = round(float64(ms.TotalAlloc-alloc0) / (1 << 20))
	out["epochs_run"] = m.Net.Epochs
	out["weights"] = weights(m)
	opts.report(m, out)

	raw, err := m.Encode()
	if err != nil {
		fail(err)
	}
	out["artifact_bytes"] = len(raw)
	return raw
}

// measureScoring records in out the batch throughput, the latency of
// scoring one row at a time, and the test accuracy and ROC AUC.
func measureScoring(m *learn.Model, xTest *learn.Frame, yTest []float64, latencyRows int, out map[string]any) {
	start := time.Now()
	proba := m.Proba(xTest)
	secs := time.Since(start).Seconds()
	out["batch_rows_per_s"] = math.Round(float64(xTest.N) / secs)

	lat := make([]float64, 0, latencyRows)
	for i := range min(latencyRows, xTest.N) {
		one := xTest.Take([]int{i})
		t := time.Now()
		m.Proba(one)
		lat = append(lat, float64(time.Since(t).Nanoseconds())/1e3)
	}
	slices.Sort(lat)
	out["latency_p50_us"] = round(lat[len(lat)/2])
	out["latency_p95_us"] = round(lat[len(lat)*95/100])

	pred := make([]float64, len(proba))
	for i, p := range proba {
		if p[1] > p[0] {
			pred[i] = 1
		}
	}
	metrics := learn.ClassificationMetrics(yTest, pred, proba, 2)
	out["accuracy"], out["roc_auc"] = round4(metrics["accuracy"]), round4(metrics["roc_auc"])
}

// dataset is n rows of 40 columns: 30 numbers (5% missing), 8 text columns
// of 3 to 10 values and 2 ID-like text columns of 500 and 2000 values, and a
// yes/no target that depends on some of each, with label noise.
func dataset(n int, seed uint64) (*learn.Frame, []float64) {
	var names, kinds []string
	for j := range 30 {
		names, kinds = append(names, "n"+strconv.Itoa(j)), append(kinds, learn.KindNumber)
	}
	for j := range 10 {
		names, kinds = append(names, "s"+strconv.Itoa(j)), append(kinds, learn.KindString)
	}
	f := learn.NewFrame(names, kinds, n)
	y := make([]float64, n)
	rng := rand.New(rand.NewPCG(seed, 99)) //nolint:gosec // G404: seeded on purpose, so every run generates the same table.
	// Each ID-like value has its own effect on the target.
	effect := make([]float64, 2000)
	for i := range effect {
		effect[i] = rng.NormFloat64()
	}
	for i := range n {
		for j := range 30 {
			if rng.Float64() < 0.05 {
				continue
			}
			f.Num[j][i] = rng.NormFloat64()
		}
		for j := range 8 {
			f.Str[30+j][i] = "v" + strconv.Itoa(rng.IntN(3+j))
		}
		a, b := rng.IntN(500), rng.IntN(2000)
		f.Str[38][i], f.Str[39][i] = "a"+strconv.Itoa(a), "b"+strconv.Itoa(b)
		x0, x1, x2 := orZero(f.Num[0][i]), orZero(f.Num[1][i]), orZero(f.Num[2][i])
		z := x0 + x1*x2 + math.Sin(2*orZero(f.Num[3][i])) + 0.5*effect[a] + 0.5*effect[b]
		if strings.HasSuffix(f.Str[30][i], "0") {
			z += 0.7
		}
		z += 0.5 * rng.NormFloat64()
		if z > 0 {
			y[i] = 1
		}
	}
	return f, y
}

func orZero(v float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	return v
}

func pick(v []float64, idx []int) []float64 {
	out := make([]float64, len(idx))
	for i, j := range idx {
		out[i] = v[j]
	}
	return out
}

func yTrainOf(y []float64, idx []int) []float64 { return pick(y, idx) }

// majority is the accuracy on test of always answering train's most common
// class.
func majority(train, test []float64) float64 {
	ones := 0
	for _, v := range train {
		if v == 1 {
			ones++
		}
	}
	guess := 0.0
	if 2*ones > len(train) {
		guess = 1
	}
	right := 0
	for _, v := range test {
		if v == guess {
			right++
		}
	}
	return float64(right) / float64(len(test))
}

// weights counts a float64 network's weights and biases.
func weights(m *learn.Model) int {
	n := 0
	for _, l := range m.Net.Layers {
		n += len(l.B)
		for _, r := range l.W {
			n += len(r)
		}
	}
	return n
}

// status reads a field of /proc/self/status, in kB; 0 off Linux.
func status(field string) int64 {
	raw, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if rest, ok := strings.CutPrefix(line, field+":"); ok {
			kb, _ := strconv.ParseInt(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(rest), "kB")), 10, 64)
			return kb
		}
	}
	return 0
}

// resetPeak sets VmHWM back to the current resident set (Linux 4.0+).
func resetPeak() { _ = os.WriteFile("/proc/self/clear_refs", []byte("5"), 0) }

func cpuSeconds() float64 {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0
	}
	return float64(ru.Utime.Sec+ru.Stime.Sec) + float64(ru.Utime.Usec+ru.Stime.Usec)/1e6
}

func mb(kb int64) float64 { return round(float64(kb) / 1024) }

func round(v float64) float64  { return math.Round(v*10) / 10 }
func round4(v float64) float64 { return math.Round(v*1e4) / 1e4 }

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
