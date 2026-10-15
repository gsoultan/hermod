// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

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
