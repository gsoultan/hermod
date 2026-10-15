// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package main

import (
	"flag"

	"github.com/gsoultan/hermod/pkg/ml/builtin/learn"
)

// options are the network's efficiency settings for a run.
type options struct {
	precision string
	threads   int
	batch     int
}

func addOptions() *options {
	o := &options{}
	flag.StringVar(&o.precision, "precision", "", "auto, float32 or int8 (default auto)")
	flag.IntVar(&o.threads, "threads", 0, "training threads (default every CPU but one)")
	flag.IntVar(&o.batch, "batch", 0, "batch size (default 32)")
	return o
}

func (o *options) apply(spec *learn.Spec) {
	spec.MLP.Precision, spec.MLP.Threads = o.precision, o.threads
	if o.batch > 0 {
		spec.MLP.BatchSize = o.batch
	}
	if spec.MLP.Precision == "" {
		spec.MLP.Precision = learn.PrecisionAuto
	}
}

func (o *options) report(m *learn.Model, out map[string]any) {
	out["precision"], out["threads"] = m.Net.Precision, m.Net.Threads
	n := 0
	for _, d := range m.Net.Dense {
		n += d.In*d.Out + d.Out
	}
	out["weights"] = n
	if q := m.Net.Quantization; q != nil {
		out["quantization"] = q
	}
}
