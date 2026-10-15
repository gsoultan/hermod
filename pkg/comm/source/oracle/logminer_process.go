// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

package oracle

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"github.com/gsoultan/hermod"
)

// V$LOGMNR_CONTENTS.OPERATION_CODE values the reader asks for.
const (
	lmOpInsert   = 1
	lmOpDelete   = 2
	lmOpUpdate   = 3
	lmOpDDL      = 5
	lmOpCommit   = 7
	lmOpRollback = 36
)

// lmValue is one column as DBMS_LOGMNR.MINE_VALUE reported it. Present is
// COLUMN_PRESENT: a column can be absent from a redo record (an update logs
// only what it changed), and MINE_VALUE alone cannot tell absent from NULL.
type lmValue struct {
	Present bool
	Null    bool
	Raw     string
}

// lmRecord is one row of V$LOGMNR_CONTENTS.
type lmRecord struct {
	SCN          uint64
	Op           int
	XID          string
	RSID         string
	SSN          int64
	Rollback     bool
	RowID        string
	Owner, Table string
	Redo, Undo   map[string]lmValue
}

// lmChange is one row change ready to become a message.
type lmChange struct {
	Owner, Table string
	Op           hermod.Operation
	Before       map[string]any
	After        map[string]any
	SCN          uint64
	RSID         string
	SSN          int64
	RowID        string
}

// lmCommitted is a committed transaction's changes on the tracked tables.
//
// Low is where a restart must resume mining for nothing to be lost once this
// transaction is delivered: the first change of the oldest transaction still
// open when it committed, or its own commit if none was.
type lmCommitted struct {
	XID       string
	CommitSCN uint64
	Low       uint64
	Changes   []lmChange
}

// lmPosition is the persisted resume point.
//
// Low is the SCN mining restarts from. A transaction whose commit is below
// CommitSCN, or at it and listed in CommitXIDs, was delivered already and is
// skipped when the re-mine meets it again. Both are needed: mining has to
// restart at the oldest open transaction's first change, which is before the
// commits of transactions delivered after it started.
type lmPosition struct {
	Low        uint64
	CommitSCN  uint64
	CommitXIDs []string
}

const (
	stateLMLow        = "logminer_scn"
	stateLMCommitSCN  = "logminer_commit_scn"
	stateLMCommitXIDs = "logminer_commit_xids"
)

func (pos lmPosition) state() map[string]string {
	return map[string]string{
		stateLMLow:        strconv.FormatUint(pos.Low, 10),
		stateLMCommitSCN:  strconv.FormatUint(pos.CommitSCN, 10),
		stateLMCommitXIDs: strings.Join(pos.CommitXIDs, ","),
	}
}

func lmPositionFromState(state map[string]string) (lmPosition, bool) {
	low, err := strconv.ParseUint(state[stateLMLow], 10, 64)
	if err != nil {
		return lmPosition{}, false
	}
	pos := lmPosition{Low: low}
	if c, err := strconv.ParseUint(state[stateLMCommitSCN], 10, 64); err == nil {
		pos.CommitSCN = c
	}
	if x := state[stateLMCommitXIDs]; x != "" {
		pos.CommitXIDs = strings.Split(x, ",")
	}
	return pos, true
}

type lmKey struct {
	SCN  uint64
	Op   int
	XID  string
	RSID string
	SSN  int64
}

type lmTxn struct {
	first   uint64
	changes []lmChange
}

// lmProcessor turns LogMiner records into committed transactions.
//
// It does not trust the redo after the last commit it has seen in the newest
// window: LogMiner reads the log files, and redo still in the log buffer is
// not in them yet. trim forgets the untrusted tail, and the next window starts
// again at that commit; records at exactly that SCN that were already
// processed are recognised and skipped.
type lmProcessor struct {
	types map[string]map[string]string
	open  map[string]*lmTxn

	resumeCommit uint64
	resumeXIDs   map[string]bool

	// seen holds the keys of processed records at SCNs at or after the last
	// commit, which is the most a re-mine from that commit can repeat.
	seen       map[uint64]map[lmKey]bool
	lastCommit uint64
	boundary   uint64
}

func newLMProcessor(pos lmPosition, types map[string]map[string]string) *lmProcessor {
	p := &lmProcessor{
		types:        types,
		open:         make(map[string]*lmTxn),
		resumeCommit: pos.CommitSCN,
		resumeXIDs:   make(map[string]bool, len(pos.CommitXIDs)),
		seen:         make(map[uint64]map[lmKey]bool),
		boundary:     pos.Low,
	}
	for _, x := range pos.CommitXIDs {
		p.resumeXIDs[x] = true
	}
	return p
}

func (p *lmProcessor) setTypes(types map[string]map[string]string) { p.types = types }

// add processes one record. It returns a transaction when r is the commit of
// one that changed a tracked table and was not delivered before a restart.
func (p *lmProcessor) add(r lmRecord) (lmCommitted, bool) {
	k := lmKey{SCN: r.SCN, Op: r.Op, XID: r.XID, RSID: r.RSID, SSN: r.SSN}
	if p.seen[r.SCN][k] {
		return lmCommitted{}, false
	}
	if r.SCN >= p.lastCommit {
		if p.seen[r.SCN] == nil {
			p.seen[r.SCN] = make(map[lmKey]bool)
		}
		p.seen[r.SCN][k] = true
	}

	switch r.Op {
	case lmOpInsert, lmOpUpdate, lmOpDelete:
		p.addChange(r)
	case lmOpRollback:
		delete(p.open, r.XID)
	case lmOpCommit:
		p.noteCommit(r.SCN)
		txn, ok := p.open[r.XID]
		if !ok {
			return lmCommitted{}, false
		}
		delete(p.open, r.XID)
		if r.SCN < p.resumeCommit || (r.SCN == p.resumeCommit && p.resumeXIDs[r.XID]) {
			return lmCommitted{}, false
		}
		if len(txn.changes) == 0 {
			return lmCommitted{}, false
		}
		return lmCommitted{XID: r.XID, CommitSCN: r.SCN, Low: p.openLow(r.SCN), Changes: txn.changes}, true
	}
	return lmCommitted{}, false
}

func (p *lmProcessor) noteCommit(scn uint64) {
	if scn <= p.lastCommit {
		return
	}
	p.lastCommit = scn
	for s := range p.seen {
		if s < scn {
			delete(p.seen, s)
		}
	}
}

func (p *lmProcessor) addChange(r lmRecord) {
	txn := p.open[r.XID]
	if r.Rollback {
		// A compensating record from ROLLBACK TO SAVEPOINT or a failed
		// statement: it undoes the latest change this transaction made to the
		// same row, which therefore never happened.
		if txn == nil {
			return
		}
		for i := len(txn.changes) - 1; i >= 0; i-- {
			if txn.changes[i].RowID == r.RowID {
				txn.changes = slices.Delete(txn.changes, i, i+1)
				break
			}
		}
		return
	}
	if txn == nil {
		txn = &lmTxn{first: r.SCN}
		p.open[r.XID] = txn
	}
	types := p.types[r.Owner+"."+r.Table]
	c := lmChange{Owner: r.Owner, Table: r.Table, SCN: r.SCN, RSID: r.RSID, SSN: r.SSN, RowID: r.RowID}
	switch r.Op {
	case lmOpInsert:
		c.Op = hermod.OpCreate
		c.After = image(nil, r.Redo, types)
	case lmOpUpdate:
		c.Op = hermod.OpUpdate
		c.Before = image(nil, r.Undo, types)
		c.After = image(c.Before, r.Redo, types)
	case lmOpDelete:
		c.Op = hermod.OpDelete
		c.Before = image(nil, r.Undo, types)
	}
	txn.changes = append(txn.changes, c)
}

// image builds a row image: base's columns, overlaid with the present ones.
func image(base map[string]any, cols map[string]lmValue, types map[string]string) map[string]any {
	out := make(map[string]any, len(base)+len(cols))
	for k, val := range base {
		out[k] = val
	}
	for name, val := range cols {
		if !val.Present {
			continue
		}
		if val.Null {
			out[name] = nil
			continue
		}
		out[name] = typedValue(types[name], val.Raw)
	}
	return out
}

// openLow is the resume point if everything up to and including the
// transaction committing at commitSCN is delivered.
func (p *lmProcessor) openLow(commitSCN uint64) uint64 {
	low := commitSCN
	for _, t := range p.open {
		if t.first < low {
			low = t.first
		}
	}
	return low
}

// trim ends a window that was trusted up to boundary: changes after it are
// forgotten (the next window, which starts at boundary, reads them again).
func (p *lmProcessor) trim(boundary uint64) uint64 {
	if boundary < p.boundary {
		boundary = p.boundary
	}
	for xid, t := range p.open {
		kept := t.changes[:0]
		for _, c := range t.changes {
			if c.SCN <= boundary {
				kept = append(kept, c)
			}
		}
		t.changes = kept
		if len(kept) == 0 {
			delete(p.open, xid)
		} else {
			t.first = kept[0].SCN
		}
	}
	for s := range p.seen {
		if s != boundary {
			delete(p.seen, s)
		}
	}
	if p.lastCommit < boundary {
		p.lastCommit = boundary
	}
	p.boundary = boundary
	return boundary
}

// low is where mining must restart, given what is open now, if every
// transaction emitted so far is delivered.
func (p *lmProcessor) low() uint64 {
	low := p.boundary
	for _, t := range p.open {
		if t.first < low {
			low = t.first
		}
	}
	return low
}

// decodeMined reads the column expression the reader selects:
// NULL when COLUMN_PRESENT is 0, otherwise 'V' followed by MINE_VALUE.
func decodeMined(s *string) lmValue {
	if s == nil || !strings.HasPrefix(*s, "V") {
		return lmValue{}
	}
	raw := (*s)[1:]
	if raw == "" {
		// Oracle has no empty string: '' is NULL.
		return lmValue{Present: true, Null: true}
	}
	return lmValue{Present: true, Raw: raw}
}

// typedValue gives a numeric column a JSON number; everything else stays the
// text MINE_VALUE returned, with dates and timestamps in the ISO formats the
// reader's session sets.
func typedValue(dataType, raw string) any {
	switch dataType {
	case "NUMBER", "FLOAT", "INTEGER", "BINARY_FLOAT", "BINARY_DOUBLE":
		if _, err := strconv.ParseFloat(raw, 64); err == nil && !strings.ContainsAny(raw, "nNiI") {
			return json.Number(raw)
		}
	}
	return raw
}
