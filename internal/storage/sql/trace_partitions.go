package sql

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

// Time-partitioning message_trace_steps on PostgreSQL.
//
// Retention on this table is a range delete, and on the table Hermod grows
// fastest that is the worst shape a delete can have: a single statement
// rewriting tens of GB of WAL, leaving dead tuples that only VACUUM FULL
// reclaims, and taking an ACCESS EXCLUSIVE lock when it finally does. A
// production database reached 50 GB here, at which point the sweep that was
// supposed to bound it had become too expensive to run safely.
//
// Dropping a partition is none of those things: a catalogue change and an
// unlink. No row-level WAL, no bloat, and the space comes back immediately.
//
// Three deliberate limits:
//
//   - PostgreSQL only. No other supported engine has declarative partitioning
//     in a form worth the branch, and they fall back to the range delete.
//   - New tables only. An existing table cannot be altered into a partitioned
//     one; it has to be rebuilt, which is an operator's decision to make
//     during a window, not something to do unattended at start-up.
//   - A DEFAULT partition always exists. If partition maintenance ever lags or
//     fails, rows land there instead of the insert failing. That degrades to
//     today's behaviour — one big table swept by DELETE — which is survivable,
//     where refusing to record a trace because tomorrow's partition is missing
//     would not be.
const (
	tracePartitionPrefix  = "message_trace_steps_p"
	tracePartitionDefault = "message_trace_steps_default"

	// tracePartitionLookahead is how many days ahead partitions are created.
	//
	// It is not tuning. Attaching a partition to a table that has a DEFAULT
	// partition makes PostgreSQL scan DEFAULT to prove no row belongs in the
	// new range, holding a lock while it does. Staying a week ahead keeps
	// DEFAULT empty, so that scan is always trivial. Cutting this to a day
	// would make every attachment wait on whatever had accumulated.
	tracePartitionLookahead = 7
)

// tracePartitioningEnabled reports whether new PostgreSQL tables are created
// partitioned. HERMOD_TRACE_PARTITIONING=off opts out.
func tracePartitioningEnabled(driver string) bool {
	if driver != "pgx" && driver != "postgres" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("HERMOD_TRACE_PARTITIONING"))) {
	case "off", "false", "0", "no":
		return false
	}
	return true
}

// tracePartitionName is the child table for one UTC day.
func tracePartitionName(day time.Time) string {
	return tracePartitionPrefix + day.UTC().Format("20060102")
}

// tracePartitionDay recovers the day from a child table name, reporting false
// for anything that is not one of ours — the DEFAULT partition above all, which
// must never be dropped.
func tracePartitionDay(name string) (time.Time, bool) {
	suffix, ok := strings.CutPrefix(name, tracePartitionPrefix)
	if !ok || len(suffix) != 8 {
		return time.Time{}, false
	}
	day, err := time.Parse("20060102", suffix)
	if err != nil {
		return time.Time{}, false
	}
	return day.UTC(), true
}

// traceStepsDDL is the CREATE TABLE Init runs for message_trace_steps.
func (s *sqlStorage) traceStepsDDL() string {
	ddl := s.queries.get(QueryInitMessageTraceStepsTable)
	if !tracePartitioningEnabled(s.driver) {
		return ddl
	}
	return ddl + " PARTITION BY RANGE (timestamp)"
}

// traceStepsIsPartitioned asks the catalogue rather than assuming, because the
// table may predate partitioning being enabled — IF NOT EXISTS leaves an
// existing unpartitioned table exactly as it was.
func (s *sqlStorage) traceStepsIsPartitioned(ctx context.Context) bool {
	if s.driver != "pgx" && s.driver != "postgres" {
		return false
	}
	var relkind string
	err := s.db.QueryRowContext(ctx,
		"SELECT relkind FROM pg_class WHERE relname = 'message_trace_steps' AND relkind IN ('r','p')").
		Scan(&relkind)
	return err == nil && relkind == "p"
}

// ensureTracePartitions creates the DEFAULT partition and the next few days'.
//
// Every statement is IF NOT EXISTS and the whole thing is best-effort: a
// failure here means rows fall into DEFAULT, not that recording breaks.
func (s *sqlStorage) ensureTracePartitions(ctx context.Context, from time.Time) error {
	if !s.traceStepsIsPartitioned(ctx) {
		return nil
	}

	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(
		"CREATE TABLE IF NOT EXISTS %s PARTITION OF message_trace_steps DEFAULT",
		tracePartitionDefault)); err != nil {
		return err
	}

	day := from.UTC().Truncate(24 * time.Hour)
	for i := range tracePartitionLookahead {
		start := day.AddDate(0, 0, i)
		end := start.AddDate(0, 0, 1)
		if _, err := s.db.ExecContext(ctx, fmt.Sprintf(
			"CREATE TABLE IF NOT EXISTS %s PARTITION OF message_trace_steps FOR VALUES FROM ('%s') TO ('%s')",
			tracePartitionName(start),
			start.Format("2006-01-02 15:04:05"),
			end.Format("2006-01-02 15:04:05"))); err != nil {
			return err
		}
	}
	return nil
}

// dropTracePartitionsBefore removes whole days that are entirely past the
// cutoff, and reports how many it dropped.
//
// A partition is only dropped when its *upper* bound is at or before the
// cutoff, so a day the cutoff falls inside is left for the range delete to
// trim. Dropping it would discard traces the retention window still covers.
func (s *sqlStorage) dropTracePartitionsBefore(ctx context.Context, before time.Time) (int, error) {
	if !s.traceStepsIsPartitioned(ctx) {
		return 0, nil
	}

	expired, err := s.expiredTracePartitions(ctx, before)
	if err != nil {
		return 0, err
	}

	dropped := 0
	for _, day := range expired {
		// The identifier is rebuilt from a time.Time via Format("20060102"), so
		// it is a fixed prefix and eight digits and cannot carry anything the
		// catalogue happened to contain. gosec tracks the taint from the
		// original rows.Scan and cannot see that the parse-and-reformat
		// launders it.
		// #nosec G701 -- prefix + time.Time formatted as YYYYMMDD; no catalogue string reaches the statement
		if _, err := s.db.ExecContext(ctx, "DROP TABLE IF EXISTS "+tracePartitionName(day)); err != nil {
			return dropped, err
		}
		dropped++
	}
	return dropped, nil
}

// expiredTracePartitions returns the days whose partitions are entirely past
// the cutoff. Days rather than names, so the caller rebuilds the identifier
// from a formatted date instead of concatenating a string read out of pg_class
// into DDL.
func (s *sqlStorage) expiredTracePartitions(ctx context.Context, before time.Time) ([]time.Time, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.relname
		FROM pg_inherits i
		JOIN pg_class c ON c.oid = i.inhrelid
		JOIN pg_class p ON p.oid = i.inhparent
		WHERE p.relname = 'message_trace_steps'`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var expired []time.Time
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		day, ok := tracePartitionDay(name)
		if !ok {
			continue
		}
		// Only when the whole day is past the cutoff. The day the cutoff falls
		// inside still holds traces the retention window covers; the range
		// delete trims those.
		if !day.AddDate(0, 0, 1).After(before.UTC()) {
			expired = append(expired, day)
		}
	}
	return expired, rows.Err()
}
