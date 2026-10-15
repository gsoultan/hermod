// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

package engine_test

import (
	"context"
	"io"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/buffer"
	"github.com/gsoultan/hermod/pkg/comm/formatter/json"
	"github.com/gsoultan/hermod/pkg/comm/sink/stdout"
	"github.com/gsoultan/hermod/pkg/engine"
	"github.com/gsoultan/hermod/pkg/engine/config"
)

// docs/usage.md shows this under "As a Library". It lived only in the docs and
// named three packages that do not exist -- pkg/sink/stdout and pkg/buffer are
// really pkg/comm/sink/stdout and pkg/comm/buffer -- and passed no formatter to
// a constructor that requires one. Nothing caught it because the module path in
// go.mod matched no repository, so the example could not have been compiled by
// anyone, including whoever wrote it.
//
// It is a compiled example now, in an external test package so the imports read
// the way a caller's would. There is no "Output:" comment on purpose: `go test`
// compiles an example without one but does not run it, which is what this
// needs -- Start blocks on a real source, and the thing worth checking is that
// the code a new user copies still builds.
func Example() {
	source := exampleSource{}
	sinks := []hermod.Sink{stdout.NewStdoutSink(json.NewJSONFormatter())}
	buf := buffer.NewRingBuffer(1024)

	eng := engine.NewEngine(source, sinks, buf)
	eng.SetConfig(config.Config{
		MaxRetries:    5,
		RetryInterval: 200 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_ = eng.Start(ctx)
}

// exampleSource stands in for a real source. Read returning io.EOF is what a
// finite source does once it is drained.
type exampleSource struct{}

func (exampleSource) Read(context.Context) (hermod.Message, error) { return nil, io.EOF }
func (exampleSource) Ack(context.Context, hermod.Message) error    { return nil }
func (exampleSource) Ping(context.Context) error                   { return nil }
func (exampleSource) Close() error                                 { return nil }
