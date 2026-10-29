// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package worker

import (
	"context"
	"errors"
	"runtime/debug"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/engine/telemetry"
)

const (
	// liveStatusWatchedInterval is how often a worker reports while someone has
	// one of its workflows open: the pace the platform pushes a local watched
	// workflow at, so the editor derives rates the same way for both.
	liveStatusWatchedInterval = time.Second
	// liveStatusIdleInterval is how often it checks in otherwise, which bounds
	// how long an editor opened on a remote workflow waits for its first frame.
	liveStatusIdleInterval = 5 * time.Second
	// liveStatusUnsupportedInterval is how often it asks a platform that has
	// said it does not take live status, in case that platform is upgraded.
	liveStatusUnsupportedInterval = time.Minute
)

// ErrLiveStatusUnsupported is returned by a platform from before the
// live-status endpoint.
var ErrLiveStatusUnsupported = errors.New("platform does not accept live status")

// liveStatusSource is the registry as the relay uses it.
type liveStatusSource interface {
	RunningWorkflowIDs() []string
	GetWorkflowStatus(id string) (telemetry.StatusUpdate, bool)
	SetRemotelyWatched(ids []string)
}

// liveStatusReporter is the platform as the relay uses it; WorkerAPIClient is one.
type liveStatusReporter interface {
	ReportLiveStatus(ctx context.Context, workerID string, report telemetry.LiveStatusReport) (telemetry.LiveStatusReply, error)
}

// liveStatusRelay sends the platform the live status of this worker's workflows
// that someone has open there.
//
// Editors subscribe to status on the platform, and a workflow running on a
// remote worker has no engine there, so its edges drew "no data". The worker
// cannot see who is watching, so it asks: each report lists every workflow it
// runs and carries frames for those the previous reply said are open.
type liveStatusRelay struct {
	workerID string
	source   liveStatusSource
	reporter liveStatusReporter
	logger   hermod.Logger

	// watched is what the platform last said is open. Only run touches it.
	watched []string
}

// run reports until ctx ends, at the pace once asks for.
func (l *liveStatusRelay) run(ctx context.Context) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			l.source.SetRemotelyWatched(nil)
			return
		case <-timer.C:
			timer.Reset(l.safeOnce(ctx))
		}
	}
}

// safeOnce is once, made to survive a panic: a fault in reporting live status
// must not take the worker's engines with it.
func (l *liveStatusRelay) safeOnce(ctx context.Context) (next time.Duration) {
	defer func() {
		if p := recover(); p != nil {
			if l.logger != nil {
				l.logger.Error("Worker: live status relay panicked", "panic", p, "stack", string(debug.Stack()))
			}
			next = liveStatusIdleInterval
		}
	}()
	return l.once(ctx)
}

// once sends one report and returns how long to wait before the next.
func (l *liveStatusRelay) once(ctx context.Context) time.Duration {
	running := l.source.RunningWorkflowIDs()
	if len(running) == 0 {
		l.setWatched(nil)
		return liveStatusIdleInterval
	}

	report := telemetry.LiveStatusReport{Running: running}
	for _, id := range l.watched {
		status, ok := l.source.GetWorkflowStatus(id)
		if !ok {
			continue
		}
		status.WorkflowID = id
		report.Updates = append(report.Updates, status)
	}

	reply, err := l.reporter.ReportLiveStatus(ctx, l.workerID, report)
	if err != nil {
		// Nobody can be watching frames that cannot arrive, so stop sampling.
		l.setWatched(nil)
		if errors.Is(err, ErrLiveStatusUnsupported) {
			return liveStatusUnsupportedInterval
		}
		if l.logger != nil && ctx.Err() == nil {
			l.logger.Debug("Worker: live status report failed", "error", err)
		}
		return liveStatusIdleInterval
	}

	l.setWatched(reply.Watched)
	if len(reply.Watched) > 0 {
		return liveStatusWatchedInterval
	}
	return liveStatusIdleInterval
}

func (l *liveStatusRelay) setWatched(ids []string) {
	l.watched = ids
	l.source.SetRemotelyWatched(ids)
}

// startLiveStatusRelay starts reporting live status until ctx ends, and
// reports whether it did.
//
// Only a worker backed by the platform API has somewhere to report to. One on a
// shared database runs beside a platform it cannot reach, so its workflows
// still draw "no data" there.
func (w *Worker) startLiveStatusRelay(ctx context.Context) bool {
	reporter, ok := w.storage.(liveStatusReporter)
	if !ok || w.workerGUID == "" {
		return false
	}
	source := w.relaySource
	if source == nil {
		if w.registry == nil {
			return false
		}
		source = w.registry
	}
	relay := &liveStatusRelay{workerID: w.workerGUID, source: source, reporter: reporter, logger: w.logger}
	go relay.run(ctx)
	return true
}
