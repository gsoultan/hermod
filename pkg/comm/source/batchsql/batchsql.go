package batchsql

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"maps"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/comm/message"
	sourcebuf "github.com/user/hermod/pkg/comm/source"
	"github.com/user/hermod/pkg/infra/sqlutil"
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

func (s *BatchSQLSource) runBatch(ctx context.Context) {
	s.log("INFO", "Starting scheduled batch SQL job", "source_id", s.config.SourceID)

	db, _, err := s.dbProvider.GetOrOpenDBByID(ctx, s.config.SourceID)
	if err != nil || db == nil {
		if err == nil {
			err = fmt.Errorf("database not found for source id: %s", s.config.SourceID)
		}
		s.log("ERROR", "Failed to get database for batch job", "error", err)
		select {
		case s.errCh <- err:
		default:
		}
		return
	}

	var queries []string
	if err := json.Unmarshal([]byte(s.config.Queries), &queries); err != nil {
		// Try parsing as single string if not JSON array
		queries = []string{s.config.Queries}
	}

	s.mu.Lock()
	lastValue := s.state["last_value"]
	s.mu.Unlock()

	count := 0
	newLastValue := lastValue

	for _, q := range queries {
		// Replace template variable
		q = strings.ReplaceAll(q, "{{.last_value}}", lastValue)
		s.log("DEBUG", "Executing batch SQL query", "query", q)

		rows, err := db.QueryContext(ctx, q)
		if err != nil {
			s.log("ERROR", "Failed to execute batch SQL query", "query", q, "error", err)
			continue
		}

		cols, err := rows.Columns()
		if err != nil {
			rows.Close()
			continue
		}

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
				val := values[i]
				if b, ok := val.([]byte); ok {
					val = string(b)
				}
				msg.SetData(colName, val)

				if s.config.IncrementalColumn != "" && colName == s.config.IncrementalColumn {
					currentVal := fmt.Sprintf("%v", val)
					if currentVal > newLastValue {
						newLastValue = currentVal
					}
				}
			}

			// Persist the watermark before emitting the message so that any
			// consumer observing this message is guaranteed to also observe the
			// updated state (avoids a read-before-write ordering race).
			s.mu.Lock()
			s.state["last_value"] = newLastValue
			s.mu.Unlock()

			select {
			case s.msgCh <- msg:
				count++
			case <-ctx.Done():
				message.ReleaseMessage(msg)
				rows.Close()
				return
			}
		}
		rows.Close()
	}

	s.mu.Lock()
	s.state["last_value"] = newLastValue
	s.mu.Unlock()

	s.log("INFO", "Completed batch SQL job", "source_id", s.config.SourceID, "records_found", count)
}

// Ack is a no-op for BatchSQLSource.
func (s *BatchSQLSource) Ack(ctx context.Context, msg hermod.Message) error {
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

// Sample fetches a single record from the specified table for preview.
func (s *BatchSQLSource) Sample(ctx context.Context, table string) (hermod.Message, error) {
	db, driver, err := s.dbProvider.GetOrOpenDBByID(ctx, s.config.SourceID)
	if err != nil {
		return nil, fmt.Errorf("failed to get database for sampling: %w", err)
	}

	quoted, err := sqlutil.QuoteIdent(driver, table)
	if err != nil {
		quoted = table
	}

	query := fmt.Sprintf("SELECT * FROM %s LIMIT 1", quoted)
	s.log("DEBUG", "Executing sample query", "query", query)

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query sample record: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		return nil, fmt.Errorf("no records found in table %s", table)
	}

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	values := make([]any, len(cols))
	valuePtrs := make([]any, len(cols))
	for i := range values {
		valuePtrs[i] = &values[i]
	}

	if err := rows.Scan(valuePtrs...); err != nil {
		return nil, fmt.Errorf("failed to scan sample record: %w", err)
	}

	msg := message.AcquireMessage()
	msg.SetID(fmt.Sprintf("sample-%s-%d", table, time.Now().Unix()))
	msg.SetOperation(hermod.OpSnapshot)
	msg.SetTable(table)
	msg.SetMetadata("sample", "true")

	for i, colName := range cols {
		val := values[i]
		if b, ok := val.([]byte); ok {
			val = string(b)
		}
		msg.SetData(colName, val)
	}

	return msg, nil
}
