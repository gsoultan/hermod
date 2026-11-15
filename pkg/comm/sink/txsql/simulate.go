// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package txsql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/infra/sqlutil"
)

var _ hermod.SimulatingSink = (*Sink)(nil)

// identityNote is said once in every report: the one thing a rolled-back
// simulation still changes.
const identityNote = "Nothing was committed. Sequences and identity columns may still have advanced, " +
	"so the next real ids can skip the ones this test used."

// Simulate runs the statement list for msg against the real database and
// rolls it back, reporting what each statement bound, returned and changed.
//
// Inside a preview run it works in the run's sandbox, so it sees what earlier
// nodes wrote; the run's rollback undoes it. Otherwise it opens a transaction
// of its own and rolls that back before it returns.
func (s *Sink) Simulate(ctx context.Context, msg hermod.Message) (rep hermod.SinkSimulation, err error) {
	rep = hermod.SinkSimulation{Notes: []string{identityNote}}
	if msg == nil {
		rep.RolledBack = true
		return rep, nil
	}
	// Always inside a preview sandbox, so the statements run behind the same
	// guard and lock-wait cap a workflow preview has. A caller without one
	// gets one of its own, rolled back before this returns.
	if !sqlutil.Sandboxed(ctx) {
		sb := sqlutil.NewSandbox(ctx)
		ctx = sqlutil.WithSandbox(ctx, sb)
		defer func() {
			if err := sb.Rollback(); err != nil {
				rep.RolledBack = false
				rep.Error = errors.Join(errorOf(rep.Error), fmt.Errorf("rolling the test back failed: %w", err)).Error()
			}
		}()
	}
	db, err := sqlutil.Use(ctx, s.db, s.driver)
	if err != nil {
		return rep, fmt.Errorf("txsql: simulate: %w", err)
	}
	tx, err := db.BeginTx(ctx, s.cfg.txOptions())
	if err != nil {
		return rep, fmt.Errorf("txsql: simulate: begin transaction: %w", err)
	}

	rec := &recorder{}
	runErr := s.runMessage(ctx, tx, msg, rec)
	rep.Steps = rec.steps
	if runErr != nil {
		rep.Error = runErr.Error()
	}
	if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
		rep.Error = errors.Join(runErr, fmt.Errorf("rolling the test back failed: %w", rbErr)).Error()
		return rep, nil
	}
	rep.RolledBack = true
	return rep, nil
}

func errorOf(s string) error {
	if s == "" {
		return nil
	}
	return errors.New(s)
}

// recorder collects what a simulated run did, step by step. A nil recorder
// records nothing, which is what a real write passes.
type recorder struct {
	steps []hermod.SimulatedStep
}

// at starts a step. It returns nil for a nil recorder, and every stepRecord
// method is a no-op on nil, so the write path pays nothing for it.
func (r *recorder) at(name string, item int) *stepRecord {
	if r == nil {
		return nil
	}
	r.steps = append(r.steps, hermod.SimulatedStep{Name: name, Item: item, Affected: -1})
	return &stepRecord{r: r, i: len(r.steps) - 1, start: time.Now()}
}

// failed records a step that failed before it could run, such as a forEach
// list that is not a list.
func (r *recorder) failed(name string, err error) {
	if r == nil {
		return
	}
	r.steps = append(r.steps, hermod.SimulatedStep{Name: name, Affected: -1, Error: err.Error()})
}

type stepRecord struct {
	r     *recorder
	i     int
	start time.Time
}

func (p *stepRecord) step() *hermod.SimulatedStep { return &p.r.steps[p.i] }

func (p *stepRecord) bound(b sqlutil.TemplateBinding) {
	if p == nil {
		return
	}
	st := p.step()
	st.SQL, st.Args = b.SQL, b.Args
}

func (p *stepRecord) returned(rows []map[string]any, total int64) {
	if p == nil {
		return
	}
	if len(rows) > hermod.MaxSimulatedRows {
		rows = rows[:hermod.MaxSimulatedRows]
	}
	st := p.step()
	st.Rows, st.RowCount = rows, total
}

func (p *stepRecord) changed(n int64, err error) {
	if p == nil || err != nil {
		return
	}
	p.step().Affected = n
}

func (p *stepRecord) done(err error) {
	if p == nil {
		return
	}
	st := p.step()
	st.DurationMs = time.Since(p.start).Milliseconds()
	if err != nil {
		st.Error = err.Error()
	}
}
