package batchsql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
	sourcebuf "github.com/gsoultan/hermod/pkg/comm/source"
	"github.com/gsoultan/hermod/pkg/infra/sqlutil"
	"github.com/robfig/cron/v3"
)

// DBProvider defines the interface for obtaining database connections.
type DBProvider interface {
	GetOrOpenDBByID(ctx context.Context, id string) (*sql.DB, string, error)
}

// Config defines the configuration for BatchSQLSource.
type Config struct {
	SourceID          string `json:"source_id"`
	Cron              string `json:"cron"`
	Queries           string `json:"queries"`
	IncrementalColumn string `json:"incremental_column"`
	// Parameters is a JSON object of named values bound into the queries. A
	// batch_sql source has no inbound message, so a {{ }} token other than
	// {{.last_value}} can only come from here.
	Parameters string `json:"parameters"`
}

// BatchSQLSource implements the hermod.Source interface for scheduled SQL queries.
type BatchSQLSource struct {
	dbProvider DBProvider
	config     Config
	cron       *cron.Cron
	msgCh      chan hermod.Message
	errCh      chan error
	mu         sync.Mutex
	logger     hermod.Logger
	started    bool
	state      map[string]string
}

// NewBatchSQLSource creates a new BatchSQLSource.
func NewBatchSQLSource(dbProvider DBProvider, config Config) *BatchSQLSource {
	var opts []cron.Option
	if len(strings.Fields(config.Cron)) > 5 {
		opts = append(opts, cron.WithSeconds())
	}
	return &BatchSQLSource{
		dbProvider: dbProvider,
		config:     config,
		cron:       cron.New(opts...),
		msgCh:      make(chan hermod.Message, sourcebuf.DefaultSourceBuffer),
		errCh:      make(chan error, 10),
		state:      make(map[string]string),
	}
}

// SetLogger sets the logger for the source.
func (s *BatchSQLSource) SetLogger(logger hermod.Logger) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logger = logger
}

func (s *BatchSQLSource) getLogger() hermod.Logger {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.logger
}

func (s *BatchSQLSource) log(level, msg string, keysAndValues ...any) {
	logger := s.getLogger()
	if logger == nil {
		return
	}

	switch level {
	case "DEBUG":
		logger.Debug(msg, keysAndValues...)
	case "INFO":
		logger.Info(msg, keysAndValues...)
	case "WARN":
		logger.Warn(msg, keysAndValues...)
	case "ERROR":
		logger.Error(msg, keysAndValues...)
	}
}

// SetState sets the initial state for incremental tracking.
func (s *BatchSQLSource) SetState(state map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if state != nil {
		// Clone to avoid aliasing the caller's map, which would let external
		// code mutate our state concurrently with runBatch (data race).
		s.state = maps.Clone(state)
	}
}

// GetState returns the current state for persistence.
func (s *BatchSQLSource) GetState() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Return a copy so callers can read/iterate safely while runBatch keeps
	// updating the internal state under the same lock.
	return maps.Clone(s.state)
}

// Read blocks until the next batch of results is available.
func (s *BatchSQLSource) Read(ctx context.Context) (hermod.Message, error) {
	s.mu.Lock()
	var err error
	var justStarted bool
	if !s.started {
		_, err = s.cron.AddFunc(s.config.Cron, func() {
			s.runBatch(context.Background())
		})
		if err == nil {
			s.cron.Start()
			s.started = true
			justStarted = true
		}
	}
	logger := s.logger
	s.mu.Unlock()

	if err != nil {
		return nil, fmt.Errorf("failed to schedule batch SQL job: %w", err)
	}
	if justStarted && logger != nil {
		logger.Info("Scheduled batch SQL job", "schedule", s.config.Cron, "source_id", s.config.SourceID)
	}

	select {
	case msg := <-s.msgCh:
		return msg, nil
	case err := <-s.errCh:
		return nil, err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// emitError publishes a non-fatal error without blocking the cron goroutine
// when nothing is draining the channel.
func (s *BatchSQLSource) emitError(err error) {
	select {
	case s.errCh <- err:
	default:
	}
}

func (s *BatchSQLSource) runBatch(ctx context.Context) {
	s.log("INFO", "Starting scheduled batch SQL job", "source_id", s.config.SourceID)

	db, driver, err := s.dbProvider.GetOrOpenDBByID(ctx, s.config.SourceID)
	if err != nil || db == nil {
		if err == nil {
			err = fmt.Errorf("database not found for source id: %s", s.config.SourceID)
		}
		s.log("ERROR", "Failed to get database for batch job", "error", err)
		s.emitError(err)
		return
	}

	queries := s.configuredQueries()

	s.mu.Lock()
	lastValue := s.state["last_value"]
	s.mu.Unlock()

	count := 0

	for _, q := range queries {
		q, args, err := s.prepareQuery(driver, q, lastValue)
		if err != nil {
			s.log("ERROR", "Failed to prepare batch SQL query", "query", q, "error", err)
			s.emitError(err)
			continue
		}
		s.log("DEBUG", "Executing batch SQL query", "query", q, "args", len(args))

		rows, err := db.QueryContext(ctx, q, args...)
		if err != nil {
			s.log("ERROR", "Failed to execute batch SQL query", "query", q, "error", err)
			continue
		}

		cols, err := rows.Columns()
		if err != nil {
			rows.Close()
			continue
		}
		// Read once, outside the row loop: the description is the same for
		// every row, and ColumnTypes allocates per call.
		typeNames := sqlutil.ColumnTypeNames(rows)

		for rows.Next() {
			values := make([]any, len(cols))
			valuePtrs := make([]any, len(cols))
			for i := range values {
				valuePtrs[i] = &values[i]
			}

			if err := rows.Scan(valuePtrs...); err != nil {
				continue
			}

			msg := message.AcquireMessage()
			msg.SetID(uuid.New().String())
			msg.SetOperation(hermod.OpSnapshot)

			for i, colName := range cols {
				// batch_sql borrows a *sql.DB from the source it delegates to
				// and scans generically, so a json/jsonb column reaches here
				// as the driver's raw bytes. Decoding it is what gives the
				// document fields for a field picker, a sink mapping and a
				// trace to reach into.
				val := sqlutil.DecodeValue(values[i], i < len(typeNames) && sqlutil.IsJSONColumnType(typeNames[i]))
				msg.SetData(colName, val)

				// The watermark this row represents travels on the message,
				// and acknowledging it is what moves the persisted cursor.
				// The read loop must not move it: GetState is the engine's
				// persistence contract, and a cursor that advanced here was
				// already past rows still in flight — a crash before the
				// sinks wrote them erased them from the resume.
				//
				// It is deliberately built from the *undecoded* value. The
				// cursor is compared against the column by the next query, so
				// it has to keep the column's own text: a decoded document
				// formats as "map[addr:map[city:London]]" and would never
				// match again.
				if s.config.IncrementalColumn != "" && colName == s.config.IncrementalColumn {
					msg.SetMetadata(watermarkKey, fmt.Sprintf("%v", sqlutil.DecodeValue(values[i], false)))
				}
			}

			select {
			case s.msgCh <- msg:
				count++
			case <-ctx.Done():
				message.ReleaseMessage(msg)
				rows.Close()
				return
			}
		}
		// A query that fails part-way through leaves a partial batch behind.
		// Saying so is what keeps the run from looking like a short table.
		if err := rows.Err(); err != nil {
			s.log("ERROR", "Batch SQL query failed while reading rows", "query", q, "error", err)
			s.emitError(err)
		}
		rows.Close()
	}

	s.log("INFO", "Completed batch SQL job", "source_id", s.config.SourceID, "records_found", count)
}

// watermarkKey is the metadata key carrying a row's incremental-column value.
const watermarkKey = "batchsql_last_value"

// configuredQueries decodes Config.Queries, which the editor writes as a JSON
// array of whole statements but which older configurations (and hand-written
// ones) may carry as a single bare SQL string.
//
// The scheduled run and the editor's preview both need this, and a preview that
// decoded the list differently would show fields the pipeline never produces.
func (s *BatchSQLSource) configuredQueries() []string {
	var queries []string
	if err := json.Unmarshal([]byte(s.config.Queries), &queries); err != nil {
		// Try parsing as single string if not JSON array
		return []string{s.config.Queries}
	}
	return queries
}

// parameters decodes the configured parameter object.
//
// A malformed blob is an error rather than an empty map: reading it as "no
// parameters" would drop every filter the operator configured and leave a query
// that still runs.
func (s *BatchSQLSource) parameters() (map[string]any, error) {
	raw := strings.TrimSpace(s.config.Parameters)
	if raw == "" {
		return nil, nil
	}
	var params map[string]any
	if err := json.Unmarshal([]byte(raw), &params); err != nil {
		return nil, fmt.Errorf("batch_sql parameters must be a JSON object: %w", err)
	}
	return params, nil
}

// prepareQuery turns a configured query into an executable statement.
//
// The incremental watermark is spliced in as text because operators write it
// inside their own quoting (`id > '{{.last_value}}'`), and that predates this
// function. Every other token is bound as a parameter, so a list variable can
// expand into an IN list -- `id IN ({{.ids}})` -- instead of being pasted into
// the SQL.
//
// An undefined token is an error. Binding it as NULL would turn a typo into a
// query that runs and matches nothing, which reads as an empty source rather
// than as a misconfiguration.
func (s *BatchSQLSource) prepareQuery(driver, query, lastValue string) (string, []any, error) {
	query = resolveLastValue(query, lastValue)
	if !strings.Contains(query, "{{") {
		return query, nil, nil
	}
	params, err := s.parameters()
	if err != nil {
		return "", nil, err
	}
	b := sqlutil.ParameterizeTemplateEx(driver, query, params)
	if b.Err != nil {
		return "", nil, b.Err
	}
	if len(b.Unresolved) > 0 {
		return "", nil, fmt.Errorf(
			"query references undefined parameter(s): %s -- define them in the source's Parameters, or remove the token",
			strings.Join(b.Unresolved, ", "))
	}
	return b.SQL, b.Args, nil
}

// resolveLastValue substitutes the incremental watermark into a query. A query
// still carrying the raw token is not valid SQL, so the preview has to
// substitute it exactly as the scheduled run does — with the empty string when
// no run has happened yet, which is what the first scheduled run also sees.
func resolveLastValue(query, lastValue string) string {
	return strings.ReplaceAll(query, "{{.last_value}}", lastValue)
}

// maxWatermark returns the larger of two watermark values, numerically when
// both sides are numbers. The maximum used to be taken on strings, where
// "10" < "9": on a numeric column the cursor stuck at 9 forever and every
// later row was re-selected by every scheduled run.
func maxWatermark(current, candidate string) string {
	if candidate == "" {
		return current
	}
	if current == "" {
		return candidate
	}
	a, errA := strconv.ParseFloat(current, 64)
	b, errB := strconv.ParseFloat(candidate, 64)
	if errA == nil && errB == nil {
		if b > a {
			return candidate
		}
		return current
	}
	if candidate > current {
		return candidate
	}
	return current
}

// Ack moves the cursor to the acknowledged row's watermark — before releasing
// the message, whose metadata carries it.
func (s *BatchSQLSource) Ack(ctx context.Context, msg hermod.Message) error {
	// The conformance suite feeds every source a nil ack, because a worker
	// goroutine dereferencing one takes the whole engine down.
	if msg == nil {
		return nil
	}
	if v := msg.Metadata()[watermarkKey]; v != "" {
		s.mu.Lock()
		s.state["last_value"] = maxWatermark(s.state["last_value"], v)
		s.mu.Unlock()
	}
	if m, ok := msg.(*message.DefaultMessage); ok {
		message.ReleaseMessage(m)
	}
	return nil
}

// Ping checks if the schedule is valid.
func (s *BatchSQLSource) Ping(ctx context.Context) error {
	if len(strings.Fields(s.config.Cron)) > 5 {
		_, err := cron.NewParser(cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor).Parse(s.config.Cron)
		return err
	}
	_, err := cron.ParseStandard(s.config.Cron)
	return err
}

// Close stops the cron scheduler and releases resources.
func (s *BatchSQLSource) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cron != nil {
		s.cron.Stop()
	}
	return nil
}

// Sample fetches a single record for preview.
//
// A batch_sql source is query-driven and owns no table: its config carries
// `queries`, never `table` or `tables`. Callers that have a table name (the
// column browser, a sink-side preview) still pass one; the workflow editor has
// none to pass, and used to send the empty string — which built
// "SELECT * FROM  LIMIT 1" and failed as a syntax error. Downstream nodes build
// their Available Fields list from this sample, so the failure surfaced only as
// an empty field list on every node wired to a batch_sql source.
//
// With no table name the first configured query is what the pipeline will
// actually run, so that is what gets previewed — the same statement, with the
// same watermark substitution, so the previewed columns are the columns the
// scheduled run emits.
func (s *BatchSQLSource) Sample(ctx context.Context, table string) (hermod.Message, error) {
	db, driver, err := s.dbProvider.GetOrOpenDBByID(ctx, s.config.SourceID)
	if err != nil {
		return nil, fmt.Errorf("failed to get database for sampling: %w", err)
	}

	var query string
	var args []any
	if table != "" {
		quoted, err := sqlutil.QuoteIdent(driver, table)
		if err != nil {
			quoted = table
		}
		query = fmt.Sprintf("SELECT * FROM %s LIMIT 1", quoted)
	} else {
		queries := s.configuredQueries()
		if len(queries) == 0 || strings.TrimSpace(queries[0]) == "" {
			return nil, errors.New("batch_sql source has no query configured to preview; add one before fetching a sample")
		}
		s.mu.Lock()
		lastValue := s.state["last_value"]
		s.mu.Unlock()
		// Deliberately not wrapped in a LIMIT: the statement is the operator's
		// own and the dialect is whatever the delegate speaks, so SQL Server and
		// Oracle would reject the wrapper. Reading one row and closing aborts
		// the query at the driver instead.
		query, args, err = s.prepareQuery(driver, queries[0], lastValue)
		if err != nil {
			return nil, err
		}
	}
	s.log("DEBUG", "Executing sample query", "query", query)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query sample record: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		// A query that errored on the first row is not an empty result, and
		// reporting it as one sends the operator looking at their data instead
		// of at their SQL.
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("failed to read sample record: %w", err)
		}
		if table == "" {
			return nil, errors.New("the configured query returned no rows to preview")
		}
		return nil, fmt.Errorf("no records found in table %s", table)
	}

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	typeNames := sqlutil.ColumnTypeNames(rows)

	values := make([]any, len(cols))
	valuePtrs := make([]any, len(cols))
	for i := range values {
		valuePtrs[i] = &values[i]
	}

	if err := rows.Scan(valuePtrs...); err != nil {
		return nil, fmt.Errorf("failed to scan sample record: %w", err)
	}

	msg := message.AcquireMessage()
	// The query path has no table to name, and "sample--1789554373" reads as a
	// truncated identifier rather than an absent one.
	if table != "" {
		msg.SetID(fmt.Sprintf("sample-%s-%d", table, time.Now().Unix()))
		msg.SetTable(table)
	} else {
		msg.SetID(fmt.Sprintf("sample-query-%d", time.Now().Unix()))
	}
	msg.SetOperation(hermod.OpSnapshot)
	msg.SetMetadata("sample", "true")

	// This sample is what the workflow editor turns into Available Fields for
	// every downstream node, so an undecoded json/jsonb column here offers no
	// sub-paths to pick and the document looks like one opaque string.
	for i, colName := range cols {
		msg.SetData(colName, sqlutil.DecodeValue(values[i], i < len(typeNames) && sqlutil.IsJSONColumnType(typeNames[i])))
	}

	return msg, nil
}
