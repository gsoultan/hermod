// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package scale

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// stepsFromEnv reads a comma-separated list of workflow counts from key, or
// returns def. A malformed entry fails the test: a typo should not quietly
// shrink a load test into a smaller one that passes.
func stepsFromEnv(t *testing.T, key string, def []int) []int {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	var steps []int
	for field := range strings.SplitSeq(raw, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(field))
		if err != nil || n <= 0 {
			t.Fatalf("%s: %q is not a positive workflow count", key, field)
		}
		steps = append(steps, n)
	}
	return steps
}

// durationFromEnv reads a Go duration from key, or returns def.
func durationFromEnv(t *testing.T, key string, def time.Duration) time.Duration {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		t.Fatalf("%s: %q is not a positive duration", key, raw)
	}
	return d
}

// intFromEnv reads a positive integer from key, or returns def.
func intFromEnv(t *testing.T, key string, def int) int {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		t.Fatalf("%s: %q is not a positive integer", key, raw)
	}
	return n
}

// floatFromEnv reads a number in [0, 1] from key, or returns def.
func floatFromEnv(t *testing.T, key string, def float64) float64 {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil || f < 0 || f > 1 {
		t.Fatalf("%s: %q is not a number between 0 and 1", key, raw)
	}
	return f
}

// percentile returns the p-th percentile (0..100) of values by the
// nearest-rank method, or 0 for no values. values is not modified.
func percentile(values []time.Duration, p float64) time.Duration {
	if len(values) == 0 {
		return 0
	}
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	rank := int(float64(len(sorted))*p/100+0.999999) - 1
	return sorted[min(max(rank, 0), len(sorted)-1)]
}

// writeReport saves rows as JSON under HERMOD_SCALE_REPORT_DIR when it is set,
// so a run can be compared with the next one. Without it the numbers are only
// logged.
func writeReport(t *testing.T, name string, rows any) {
	t.Helper()
	dir := strings.TrimSpace(os.Getenv("HERMOD_SCALE_REPORT_DIR"))
	if dir == "" {
		return
	}
	data, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		t.Fatalf("encoding the %s report: %v", name, err)
	}
	path := filepath.Join(dir, name+".json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	t.Logf("report written to %s", path)
}

// mib formats a byte count in MiB with one decimal.
func mib(b int64) string {
	return fmt.Sprintf("%.1f MiB", float64(b)/(1<<20))
}
