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
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// CDCMethodLogMiner selects log-based CDC through Oracle LogMiner. The
// default, CDCMethodIncremental, polls each table for rows above the last
// id_field value seen and so sees inserts only.
const (
	CDCMethodIncremental = "incremental"
	CDCMethodLogMiner    = "logminer"
)

// lmMaxWindow caps how many SCNs one LogMiner session covers, so catching up
// after downtime proceeds in bounded steps instead of one huge session.
const lmMaxWindow = 50_000

// lmSnapshotBatch is how many initial-load rows one Read step queues.
const lmSnapshotBatch = 500

// Message metadata the LogMiner reader stamps, and Ack reads back.
const (
	mdLMSCN       = "oracle_scn"
	mdLMCommitSCN = "oracle_commit_scn"
	mdLMXID       = "oracle_xid"
	mdLMLow       = "oracle_low_scn"
	mdLMTxnLast   = "oracle_txn_last"
	mdLMSnapshot  = "oracle_snapshot"
)

var oraIdent = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_$#]{0,127}$`)

type lmTable struct {
	Owner, Name string
	Columns     []string
	Types       map[string]string
}

func (t lmTable) key() string { return t.Owner + "." + t.Name }

// parseLMTables reads the configured table list. A bare name belongs to the
// connecting user. Names are folded to upper case, as Oracle folds unquoted
// identifiers, and anything that is not a plain identifier is refused: these
// names are written into the LogMiner query and MINE_VALUE paths.
func parseLMTables(tables []string, defaultOwner string) ([]lmTable, error) {
	if len(tables) == 0 {
		return nil, errors.New("oracle logminer: set tables to the tables to capture (OWNER.TABLE or TABLE)")
	}
	out := make([]lmTable, 0, len(tables))
	for _, raw := range tables {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		owner, name := defaultOwner, raw
		if o, n, ok := strings.Cut(raw, "."); ok {
			owner, name = o, n
		}
		owner, name = strings.ToUpper(strings.Trim(owner, `"`)), strings.ToUpper(strings.Trim(name, `"`))
		if !oraIdent.MatchString(owner) || !oraIdent.MatchString(name) {
			return nil, fmt.Errorf("oracle logminer: %q is not a plain OWNER.TABLE name", raw)
		}
		out = append(out, lmTable{Owner: owner, Name: name})
	}
	if len(out) == 0 {
		return nil, errors.New("oracle logminer: set tables to the tables to capture (OWNER.TABLE or TABLE)")
	}
	return out, nil
}

// lmColumnExpr selects one column of one record: NULL when the record does
// not carry it, otherwise 'V' followed by its value (so a NULL value reads 'V').
func lmColumnExpr(which string, t lmTable, col string) string {
	path := fmt.Sprintf("%s.%s.%s", t.Owner, t.Name, col)
	return fmt.Sprintf("CASE WHEN DBMS_LOGMNR.COLUMN_PRESENT(%s, '%s') = 1 THEN 'V' || DBMS_LOGMNR.MINE_VALUE(%s, '%s') END",
		which, path, which, path)
}

// buildLMQuery is the V$LOGMNR_CONTENTS query for the tracked tables. Each
// table's columns are evaluated only for that table's rows.
func buildLMQuery(tables []lmTable, pdb string) (string, []colRef) {
	var sel strings.Builder
	sel.WriteString("SELECT TO_CHAR(SCN), TO_CHAR(OPERATION_CODE), RAWTOHEX(XID), RS_ID, TO_CHAR(SSN), TO_CHAR(ROLLBACK), ROW_ID, SEG_OWNER, TABLE_NAME")
	var refs []colRef
	for ti, t := range tables {
		cond := fmt.Sprintf("SEG_OWNER = '%s' AND TABLE_NAME = '%s'", t.Owner, t.Name)
		for _, c := range t.Columns {
			for _, which := range []string{"REDO_VALUE", "UNDO_VALUE"} {
				fmt.Fprintf(&sel, ",\n CASE WHEN %s THEN %s END", cond, lmColumnExpr(which, t, c))
				refs = append(refs, colRef{table: ti, column: c, redo: which == "REDO_VALUE"})
			}
		}
	}
	var tracked []string
	for _, t := range tables {
		tracked = append(tracked, fmt.Sprintf("(SEG_OWNER = '%s' AND TABLE_NAME = '%s')", t.Owner, t.Name))
	}
	where := fmt.Sprintf("SCN >= :1 AND ((OPERATION_CODE IN (%d, %d, %d, %d) AND (%s)) OR OPERATION_CODE IN (%d, %d))",
		lmOpInsert, lmOpDelete, lmOpUpdate, lmOpDDL, strings.Join(tracked, " OR "), lmOpCommit, lmOpRollback)
	if pdb != "" {
		where = fmt.Sprintf("SRC_CON_NAME = '%s' AND %s", pdb, where)
	}
	return sel.String() + "\nFROM V$LOGMNR_CONTENTS WHERE " + where, refs
}

type colRef struct {
	table  int
	column string
	redo   bool
}

// logMiner is the LogMiner reader behind an OracleSource in logminer mode.
type logMiner struct {
	src         *OracleSource
	pdb         string
	initialLoad bool

	conn         *sql.Conn
	tables       []lmTable
	query        string
	refs         []colRef
	explicitLogs bool

	mu       sync.Mutex
	proc     *lmProcessor
	started  bool
	resume   *lmPosition // from SetState, until started
	acked    lmPosition
	ackedSet bool
	inFlight int
	loadDone bool
	// snapInFlight counts initial-load rows not yet acknowledged. The
	// position is not reported until it is zero after the load finished:
	// a restart before then must load again rather than skip unsent rows.
	snapInFlight int

	queue []hermod.Message
	snap  *lmSnapshot
}

type lmSnapshot struct {
	scn   uint64
	table int
	n     int
	rows  *sql.Rows
	cols  []string
}

func newLogMiner(src *OracleSource, pdb string, initialLoad bool) *logMiner {
	return &logMiner{src: src, pdb: strings.ToUpper(strings.TrimSpace(pdb)), initialLoad: initialLoad}
}

func (lm *logMiner) setState(state map[string]string) {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	if pos, ok := lmPositionFromState(state); ok {
		lm.resume = &pos
		lm.acked = pos
		lm.ackedSet = true
		lm.loadDone = true
	}
}

func (lm *logMiner) getState() map[string]string {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	if !lm.ackedSet || !lm.loadDone || lm.snapInFlight > 0 {
		return nil
	}
	pos := lm.acked
	// With nothing in flight, everything mined so far is delivered: the
	// position can move to where the reader is, so a quiet table does not
	// pin a restart to redo that may since have been archived away.
	if lm.inFlight == 0 && lm.proc != nil && lm.loadDone {
		if l := lm.proc.low(); l > pos.Low {
			pos.Low = l
		}
	}
	return pos.state()
}

func (lm *logMiner) ack(msg hermod.Message) {
	md := msg.Metadata()
	if md[mdLMCommitSCN] == "" && md[mdLMSnapshot] == "" {
		return
	}
	lm.mu.Lock()
	defer lm.mu.Unlock()
	if lm.inFlight > 0 {
		lm.inFlight--
	}
	if md[mdLMSnapshot] != "" {
		if lm.snapInFlight > 0 {
			lm.snapInFlight--
		}
		return
	}
	if md[mdLMTxnLast] != "true" {
		return
	}
	commit, err1 := strconv.ParseUint(md[mdLMCommitSCN], 10, 64)
	low, err2 := strconv.ParseUint(md[mdLMLow], 10, 64)
	if err1 != nil || err2 != nil {
		return
	}
	switch {
	case commit > lm.acked.CommitSCN:
		lm.acked.CommitSCN = commit
		lm.acked.CommitXIDs = []string{md[mdLMXID]}
	case commit == lm.acked.CommitSCN:
		lm.acked.CommitXIDs = append(lm.acked.CommitXIDs, md[mdLMXID])
	}
	if low > lm.acked.Low {
		lm.acked.Low = low
	}
	lm.ackedSet = true
}

func (lm *logMiner) close() {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	if lm.snap != nil && lm.snap.rows != nil {
		_ = lm.snap.rows.Close()
		lm.snap = nil
	}
	if lm.conn != nil {
		_ = lm.conn.Close()
		lm.conn = nil
	}
}

// resetSession forgets the per-session setup after a failure. The processor,
// the queue and the acknowledged position are kept: mining resumes from the
// processor's boundary on a new session.
func (lm *logMiner) resetSession() {
	lm.explicitLogs = false
}

func (lm *logMiner) pop() hermod.Message {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	if len(lm.queue) == 0 {
		return nil
	}
	m := lm.queue[0]
	lm.queue[0] = nil
	lm.queue = lm.queue[1:]
	return m
}

func (lm *logMiner) push(msgs ...hermod.Message) {
	lm.mu.Lock()
	lm.queue = append(lm.queue, msgs...)
	lm.inFlight += len(msgs)
	lm.mu.Unlock()
}

// connect opens the dedicated session LogMiner runs in (a LogMiner session
// belongs to one database session) and prepares it.
func (lm *logMiner) connect(ctx context.Context, db *sql.DB) error {
	if lm.conn != nil {
		return nil
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("oracle logminer: open session: %w", err)
	}
	for _, stmt := range []string{
		`ALTER SESSION SET NLS_DATE_FORMAT = 'YYYY-MM-DD"T"HH24:MI:SS'`,
		`ALTER SESSION SET NLS_TIMESTAMP_FORMAT = 'YYYY-MM-DD"T"HH24:MI:SS.FF6'`,
		`ALTER SESSION SET NLS_TIMESTAMP_TZ_FORMAT = 'YYYY-MM-DD"T"HH24:MI:SS.FF6TZH:TZM'`,
		`ALTER SESSION SET NLS_NUMERIC_CHARACTERS = '.,'`,
	} {
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			_ = conn.Close()
			return fmt.Errorf("oracle logminer: %s: %w", stmt, err)
		}
	}
	lm.conn = conn
	if err := lm.loadTables(ctx); err != nil {
		_ = conn.Close()
		lm.conn = nil
		return err
	}
	return nil
}

// inPDB runs fn with the session switched into the configured PDB, for
// dictionary reads when mining from CDB$ROOT.
func (lm *logMiner) inPDB(ctx context.Context, fn func() error) error {
	if lm.pdb == "" {
		return fn()
	}
	if _, err := lm.conn.ExecContext(ctx, "ALTER SESSION SET CONTAINER = "+lm.pdb); err != nil {
		return fmt.Errorf("oracle logminer: switch to PDB %s: %w", lm.pdb, err)
	}
	ferr := fn()
	if _, err := lm.conn.ExecContext(ctx, "ALTER SESSION SET CONTAINER = CDB$ROOT"); err != nil {
		return errors.Join(ferr, fmt.Errorf("oracle logminer: switch back to CDB$ROOT: %w", err))
	}
	return ferr
}

func (lm *logMiner) loadTables(ctx context.Context) error {
	return lm.inPDB(ctx, func() error {
		var user string
		if err := lm.conn.QueryRowContext(ctx, "SELECT USER FROM DUAL").Scan(&user); err != nil {
			return fmt.Errorf("oracle logminer: read current user: %w", err)
		}
		tables, err := parseLMTables(lm.src.tables, user)
		if err != nil {
			return err
		}
		for i := range tables {
			if err := lm.readColumns(ctx, &tables[i], user); err != nil {
				return err
			}
		}
		lm.tables = tables
		lm.query, lm.refs = buildLMQuery(tables, lm.pdb)
		return nil
	})
}

func (lm *logMiner) readColumns(ctx context.Context, t *lmTable, user string) error {
	t.Types = make(map[string]string)
	rows, err := lm.conn.QueryContext(ctx,
		"SELECT COLUMN_NAME, DATA_TYPE FROM ALL_TAB_COLUMNS WHERE OWNER = :1 AND TABLE_NAME = :2 ORDER BY COLUMN_ID",
		t.Owner, t.Name)
	if err != nil {
		return fmt.Errorf("oracle logminer: read columns of %s: %w", t.key(), err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var col, typ string
		if err := rows.Scan(&col, &typ); err != nil {
			return err
		}
		if !oraIdent.MatchString(col) || unminable(typ) {
			// MINE_VALUE cannot read LOBs, LONGs or object types, and a
			// quoted name with odd characters cannot go in a MINE_VALUE
			// path. Such columns are left out of the images, as the docs say.
			continue
		}
		t.Columns = append(t.Columns, col)
		t.Types[col] = normalizeType(typ)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(t.Columns) == 0 {
		return fmt.Errorf("oracle logminer: table %s not found, or not visible to %s", t.key(), user)
	}
	return nil
}

func unminable(typ string) bool {
	switch {
	case strings.Contains(typ, "LOB"), strings.HasPrefix(typ, "LONG"), typ == "XMLTYPE", typ == "BFILE", typ == "JSON":
		return true
	}
	return false
}

func normalizeType(typ string) string {
	if i := strings.IndexByte(typ, '('); i > 0 {
		return typ[:i]
	}
	return typ
}

func (lm *logMiner) typeMap() map[string]map[string]string {
	m := make(map[string]map[string]string, len(lm.tables))
	for _, t := range lm.tables {
		m[t.key()] = t.Types
	}
	return m
}

// checkReady reports what stops LogMiner from producing complete row images.
func (lm *logMiner) checkReady(ctx context.Context) error {
	var minLog, allLog string
	if err := lm.conn.QueryRowContext(ctx,
		"SELECT SUPPLEMENTAL_LOG_DATA_MIN, SUPPLEMENTAL_LOG_DATA_ALL FROM V$DATABASE").Scan(&minLog, &allLog); err != nil {
		return fmt.Errorf("oracle logminer: read V$DATABASE (grant SELECT ON V_$DATABASE): %w", err)
	}
	if minLog == "NO" {
		return errors.New("oracle logminer: minimal supplemental logging is off; run ALTER DATABASE ADD SUPPLEMENTAL LOG DATA (in CDB$ROOT)")
	}
	if allLog == "YES" {
		return nil
	}
	return lm.inPDB(ctx, func() error {
		for _, t := range lm.tables {
			var n int
			if err := lm.conn.QueryRowContext(ctx,
				"SELECT COUNT(*) FROM ALL_LOG_GROUPS WHERE OWNER = :1 AND TABLE_NAME = :2 AND LOG_GROUP_TYPE = 'ALL COLUMN LOGGING'",
				t.Owner, t.Name).Scan(&n); err != nil {
				return fmt.Errorf("oracle logminer: read supplemental logging of %s: %w", t.key(), err)
			}
			if n == 0 {
				return fmt.Errorf("oracle logminer: %s has no ALL COLUMNS supplemental logging, so updates and deletes would arrive without full row images; run ALTER TABLE %s ADD SUPPLEMENTAL LOG DATA (ALL) COLUMNS",
					t.key(), t.key())
			}
		}
		return nil
	})
}

func (lm *logMiner) currentSCN(ctx context.Context) (uint64, error) {
	var s string
	if err := lm.conn.QueryRowContext(ctx, "SELECT TO_CHAR(CURRENT_SCN) FROM V$DATABASE").Scan(&s); err != nil {
		return 0, fmt.Errorf("oracle logminer: read current SCN: %w", err)
	}
	return strconv.ParseUint(s, 10, 64)
}

// step does one unit of work: start, a batch of initial load, or one mining
// window. It queues what it produced.
func (lm *logMiner) step(ctx context.Context, db *sql.DB) error {
	if err := lm.connect(ctx, db); err != nil {
		return err
	}
	if !lm.started {
		if err := lm.start(ctx); err != nil {
			return err
		}
	}
	if lm.snap != nil {
		return lm.snapshotBatch(ctx)
	}
	return lm.mine(ctx)
}

func (lm *logMiner) start(ctx context.Context) error {
	if err := lm.checkReady(ctx); err != nil {
		return err
	}
	lm.mu.Lock()
	resume := lm.resume
	lm.mu.Unlock()

	var pos lmPosition
	if resume != nil {
		pos = *resume
	} else {
		cur, err := lm.currentSCN(ctx)
		if err != nil {
			return err
		}
		pos = lmPosition{Low: cur}
		if lm.initialLoad {
			// The rows as of this SCN, then every change after it.
			lm.snap = &lmSnapshot{scn: cur}
			pos.Low = cur + 1
		}
	}
	lm.mu.Lock()
	lm.proc = newLMProcessor(pos, lm.typeMap())
	if resume == nil {
		lm.acked = lmPosition{Low: pos.Low}
		if lm.snap == nil {
			lm.loadDone = true
			lm.ackedSet = true
		}
	}
	lm.started = true
	lm.mu.Unlock()
	return nil
}

func (lm *logMiner) snapshotBatch(ctx context.Context) error {
	s := lm.snap
	if s.rows == nil {
		if s.table >= len(lm.tables) {
			lm.mu.Lock()
			lm.snap = nil
			lm.loadDone = true
			lm.ackedSet = true
			lm.mu.Unlock()
			return nil
		}
		if err := lm.openSnapshot(ctx, s); err != nil {
			return err
		}
	}
	t := lm.tables[s.table]
	msgs, err := lm.scanSnapshot(s, t)
	if err != nil {
		return fmt.Errorf("oracle logminer: initial load of %s: %w", t.key(), err)
	}
	if len(msgs) < lmSnapshotBatch {
		err := s.rows.Err()
		_ = s.rows.Close()
		if err != nil {
			return fmt.Errorf("oracle logminer: initial load of %s: %w", t.key(), err)
		}
		s.rows = nil
		s.table++
		s.n = 0
	}
	lm.mu.Lock()
	lm.snapInFlight += len(msgs)
	if s.rows == nil && s.table >= len(lm.tables) {
		lm.snap = nil
		lm.loadDone = true
		lm.ackedSet = true
	}
	lm.mu.Unlock()
	lm.push(msgs...)
	return nil
}

// openSnapshot starts the flashback query of the next table to load: the
// table as it was at the SCN mining starts after.
func (lm *logMiner) openSnapshot(ctx context.Context, s *lmSnapshot) error {
	t := lm.tables[s.table]
	cols := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		cols[i] = `"` + c + `"`
	}
	return lm.inPDB(ctx, func() error {
		for attempt := 0; ; attempt++ {
			// Owner, table and column names passed oraIdent; the SCN is a number.
			q := fmt.Sprintf(`SELECT %s FROM "%s"."%s" AS OF SCN %d`, strings.Join(cols, ", "), t.Owner, t.Name, s.scn) //nolint:gosec // G201: every identifier is checked against oraIdent and quoted
			rows, err := lm.conn.QueryContext(ctx, q)                                                                   //nolint:rowserrcheck,sqlclosecheck // held open across Reads; snapshotBatch checks Err and closes it
			if err == nil {
				s.rows, s.cols = rows, t.Columns
				return nil
			}
			// ORA-01466: the table's definition is newer than the SCN (adding
			// supplemental logging is such a change). Before the first row is
			// read nothing depends on the SCN yet, so take a later one; the
			// redo after it is what gets mined.
			if !strings.Contains(err.Error(), "ORA-01466") || s.table > 0 || attempt == 15 {
				return fmt.Errorf("oracle logminer: initial load of %s: %w", t.key(), err)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Second):
			}
			cur, err := lm.currentSCN(ctx)
			if err != nil {
				return err
			}
			s.scn = cur
			lm.mu.Lock()
			lm.proc.boundary = cur + 1
			lm.acked.Low = cur + 1
			lm.mu.Unlock()
		}
	})
}

func (lm *logMiner) scanSnapshot(s *lmSnapshot, t lmTable) ([]hermod.Message, error) {
	var msgs []hermod.Message
	vals := make([]sql.NullString, len(s.cols))
	ptrs := make([]any, len(vals))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	for len(msgs) < lmSnapshotBatch && s.rows.Next() {
		if err := s.rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		after := make(map[string]any, len(s.cols))
		for i, c := range s.cols {
			if vals[i].Valid {
				after[c] = typedValue(t.Types[c], vals[i].String)
			} else {
				after[c] = nil
			}
		}
		s.n++
		msgs = append(msgs, lmSnapshotMessage(t, after, s.scn, s.n))
	}
	return msgs, nil
}

func lmSnapshotMessage(t lmTable, after map[string]any, scn uint64, n int) hermod.Message {
	afterJSON, _ := json.Marshal(after)
	msg := message.AcquireMessage()
	msg.SetID(fmt.Sprintf("oracle:%s:snapshot:%d:%d", t.key(), scn, n))
	msg.SetOperation(hermod.OpSnapshot)
	msg.SetSchema(t.Owner)
	msg.SetTable(t.Name)
	msg.SetAfter(afterJSON)
	msg.SetMetadata("source", "oracle")
	msg.SetMetadata("snapshot", "true")
	msg.SetMetadata(mdLMSnapshot, "true")
	msg.SetMetadata(mdLMSCN, strconv.FormatUint(scn, 10))
	return msg
}

func isMissingLog(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "ORA-01291") || strings.Contains(s, "ORA-01292")
}

func (lm *logMiner) startSession(ctx context.Context, start, end uint64) error {
	const startStmt = "BEGIN DBMS_LOGMNR.START_LOGMNR(STARTSCN => :1, ENDSCN => :2, OPTIONS => DBMS_LOGMNR.DICT_FROM_ONLINE_CATALOG); END;"
	if !lm.explicitLogs {
		_, err := lm.conn.ExecContext(ctx, startStmt, int64(start), int64(end))
		if err == nil {
			return nil
		}
		if !isMissingLog(err) {
			return fmt.Errorf("oracle logminer: start: %w", err)
		}
		// Not a PDB that finds its own logs (19c, a non-CDB, or CDB$ROOT):
		// register the files covering the window ourselves from now on.
		lm.explicitLogs = true
	}
	if err := lm.addLogFiles(ctx, start, end); err != nil {
		return err
	}
	if _, err := lm.conn.ExecContext(ctx, startStmt, int64(start), int64(end)); err != nil {
		if isMissingLog(err) {
			return fmt.Errorf("oracle logminer: SCN %d is no longer in the online or archived redo logs, so changes since then cannot be read; reset the source's state to start again from now (and run an initial load if rows must not be missed): %w", start, err)
		}
		return fmt.Errorf("oracle logminer: start: %w", err)
	}
	return nil
}

type lmLogFile struct{ thread, seq, name string }

func (lm *logMiner) queryLogFiles(ctx context.Context, what, q string, args ...any) ([]lmLogFile, error) {
	rows, err := lm.conn.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("oracle logminer: list %s: %w", what, err)
	}
	defer func() { _ = rows.Close() }()
	var out []lmLogFile
	for rows.Next() {
		var f lmLogFile
		if err := rows.Scan(&f.thread, &f.seq, &f.name); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("oracle logminer: list %s: %w", what, err)
	}
	return out, nil
}

// addLogFiles registers the archived and online logs covering [start, end],
// one file per log sequence, preferring the archived copy.
func (lm *logMiner) addLogFiles(ctx context.Context, start, end uint64) error {
	archivedLogs, err := lm.queryLogFiles(ctx, "archived logs (grant SELECT ON V_$ARCHIVED_LOG)",
		`SELECT TO_CHAR(THREAD#), TO_CHAR(SEQUENCE#), MIN(NAME) FROM V$ARCHIVED_LOG
		WHERE NAME IS NOT NULL AND DELETED = 'NO' AND STATUS = 'A' AND NEXT_CHANGE# > :1 AND FIRST_CHANGE# <= :2
		GROUP BY THREAD#, SEQUENCE# ORDER BY THREAD#, SEQUENCE#`, int64(start), int64(end))
	if err != nil {
		return err
	}
	online, err := lm.queryLogFiles(ctx, "online logs (grant SELECT ON V_$LOG and V_$LOGFILE)",
		`SELECT TO_CHAR(L.THREAD#), TO_CHAR(L.SEQUENCE#), MIN(F.MEMBER) FROM V$LOG L JOIN V$LOGFILE F ON F.GROUP# = L.GROUP#
		WHERE L.STATUS IN ('CURRENT', 'ACTIVE', 'INACTIVE') AND L.FIRST_CHANGE# <= :1 AND L.NEXT_CHANGE# > :2
		GROUP BY L.THREAD#, L.SEQUENCE# ORDER BY L.THREAD#, L.SEQUENCE#`, int64(end), int64(start))
	if err != nil {
		return err
	}
	archived := make(map[string]bool, len(archivedLogs))
	files := archivedLogs
	for _, f := range archivedLogs {
		archived[f.thread+"/"+f.seq] = true
	}
	for _, f := range online {
		if !archived[f.thread+"/"+f.seq] {
			files = append(files, f)
		}
	}
	if len(files) == 0 {
		return fmt.Errorf("oracle logminer: no online or archived redo log covers SCN %d", start)
	}
	for i, f := range files {
		opt := "DBMS_LOGMNR.ADDFILE"
		if i == 0 {
			opt = "DBMS_LOGMNR.NEW"
		}
		if _, err := lm.conn.ExecContext(ctx,
			"BEGIN DBMS_LOGMNR.ADD_LOGFILE(LOGFILENAME => :1, OPTIONS => "+opt+"); END;", f.name); err != nil {
			return fmt.Errorf("oracle logminer: add log file %s: %w", f.name, err)
		}
	}
	return nil
}

// mine reads one window of redo into the processor and queues the
// transactions that committed in it.
func (lm *logMiner) mine(ctx context.Context) error {
	cur, err := lm.currentSCN(ctx)
	if err != nil {
		return err
	}
	lm.mu.Lock()
	start := lm.proc.boundary
	lm.mu.Unlock()
	if cur <= start {
		return nil
	}
	end := min(cur, start+lmMaxWindow)
	head := end == cur

	if err := lm.startSession(ctx, start, end); err != nil {
		return err
	}
	committed, ddl, err := lm.readContents(ctx, start)
	if _, endErr := lm.conn.ExecContext(context.WithoutCancel(ctx), "BEGIN DBMS_LOGMNR.END_LOGMNR; END;"); endErr != nil && err == nil {
		err = fmt.Errorf("oracle logminer: end: %w", endErr)
	}
	if err != nil {
		return err
	}

	lm.mu.Lock()
	// Behind the head, the window is in the logs whole. At the head, only
	// what precedes the newest commit is known to have been flushed.
	boundary := end
	if head {
		boundary = max(start, lm.proc.lastCommit)
	}
	lm.proc.trim(boundary)
	lm.mu.Unlock()

	for _, c := range committed {
		lm.push(lmMessages(c)...)
	}
	if ddl {
		// A tracked table changed shape: read its columns again so the next
		// window decodes the new ones.
		if err := lm.inPDBReload(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (lm *logMiner) inPDBReload(ctx context.Context) error {
	if err := lm.loadTables(ctx); err != nil {
		return err
	}
	lm.mu.Lock()
	lm.proc.setTypes(lm.typeMap())
	lm.mu.Unlock()
	return nil
}

func (lm *logMiner) readContents(ctx context.Context, start uint64) ([]lmCommitted, bool, error) {
	rows, err := lm.conn.QueryContext(ctx, lm.query, int64(start))
	if err != nil {
		return nil, false, fmt.Errorf("oracle logminer: read V$LOGMNR_CONTENTS: %w", err)
	}
	defer func() { _ = rows.Close() }()

	n := 9 + len(lm.refs)
	raw := make([]sql.NullString, n)
	ptrs := make([]any, n)
	for i := range raw {
		ptrs[i] = &raw[i]
	}
	var out []lmCommitted
	ddl := false
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return nil, false, fmt.Errorf("oracle logminer: scan V$LOGMNR_CONTENTS: %w", err)
		}
		rec, err := lmRecordFrom(raw, lm.refs, lm.tables)
		if err != nil {
			return nil, false, err
		}
		if rec.Op == lmOpDDL {
			ddl = true
			continue
		}
		lm.mu.Lock()
		c, ok := lm.proc.add(rec)
		lm.mu.Unlock()
		if ok {
			out = append(out, c)
		}
	}
	return out, ddl, rows.Err()
}

func lmRecordFrom(raw []sql.NullString, refs []colRef, tables []lmTable) (lmRecord, error) {
	scn, err := strconv.ParseUint(raw[0].String, 10, 64)
	if err != nil {
		return lmRecord{}, fmt.Errorf("oracle logminer: bad SCN %q", raw[0].String)
	}
	op, _ := strconv.Atoi(raw[1].String)
	ssn, _ := strconv.ParseInt(raw[4].String, 10, 64)
	rec := lmRecord{
		SCN: scn, Op: op, XID: raw[2].String, RSID: strings.TrimSpace(raw[3].String), SSN: ssn,
		Rollback: raw[5].String == "1", RowID: raw[6].String, Owner: raw[7].String, Table: raw[8].String,
	}
	if op != lmOpInsert && op != lmOpUpdate && op != lmOpDelete {
		return rec, nil
	}
	rec.Redo, rec.Undo = map[string]lmValue{}, map[string]lmValue{}
	for i, ref := range refs {
		t := tables[ref.table]
		if t.Owner != rec.Owner || t.Name != rec.Table {
			continue
		}
		cell := raw[9+i]
		var p *string
		if cell.Valid {
			p = &cell.String
		}
		val := decodeMined(p)
		if ref.redo {
			rec.Redo[ref.column] = val
		} else {
			rec.Undo[ref.column] = val
		}
	}
	return rec, nil
}

func lmMessages(c lmCommitted) []hermod.Message {
	msgs := make([]hermod.Message, 0, len(c.Changes))
	for i, ch := range c.Changes {
		msg := message.AcquireMessage()
		msg.SetID(fmt.Sprintf("oracle:%s.%s:%d:%s:%d", ch.Owner, ch.Table, ch.SCN, ch.RSID, ch.SSN))
		msg.SetOperation(ch.Op)
		msg.SetSchema(ch.Owner)
		msg.SetTable(ch.Table)
		if ch.Before != nil {
			b, _ := json.Marshal(ch.Before)
			msg.SetBefore(b)
		}
		if ch.After != nil {
			a, _ := json.Marshal(ch.After)
			msg.SetAfter(a)
		}
		msg.SetMetadata("source", "oracle")
		msg.SetMetadata(mdLMSCN, strconv.FormatUint(ch.SCN, 10))
		msg.SetMetadata(mdLMCommitSCN, strconv.FormatUint(c.CommitSCN, 10))
		msg.SetMetadata(mdLMXID, c.XID)
		msg.SetMetadata(mdLMLow, strconv.FormatUint(c.Low, 10))
		if i == len(c.Changes)-1 {
			msg.SetMetadata(mdLMTxnLast, "true")
		}
		msgs = append(msgs, msg)
	}
	return msgs
}
