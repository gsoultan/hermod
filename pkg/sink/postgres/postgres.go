package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
	"github.com/user/hermod/pkg/sqlutil"
	"log"
	"sync"
)

// PostgresSink implements the hermod.Sink interface for PostgreSQL.
type PostgresSink struct {
	connString string
	pool       *pgxpool.Pool
	logger     hermod.Logger
	mu         sync.Mutex
	tx         pgx.Tx
}

func NewPostgresSink(connString string) *PostgresSink {
	return &PostgresSink{
		connString: connString,
	}
}

func (s *PostgresSink) SetLogger(logger hermod.Logger) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logger = logger
}

func (s *PostgresSink) log(level, msg string, keysAndValues ...interface{}) {
	s.mu.Lock()
	logger := s.logger
	s.mu.Unlock()

	if logger == nil {
		if len(keysAndValues) > 0 {
			log.Printf("[%s] %s %v", level, msg, keysAndValues)
		} else {
			log.Printf("[%s] %s", level, msg)
		}
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

func (s *PostgresSink) Write(ctx context.Context, msg hermod.Message) error {
	return s.WriteBatch(ctx, []hermod.Message{msg})
}

func (s *PostgresSink) WriteBatch(ctx context.Context, msgs []hermod.Message) error {
	// Filter nil messages
	filtered := make([]hermod.Message, 0, len(msgs))
	for _, m := range msgs {
		if m != nil {
			filtered = append(filtered, m)
		}
	}
	msgs = filtered

	if len(msgs) == 0 {
		return nil
	}
	if s.pool == nil {
		if err := s.init(ctx); err != nil {
			return err
		}
	}

	// Group by table and operation
	groups := make(map[string][]hermod.Message)
	for _, msg := range msgs {
		table := msg.Table()
		if msg.Schema() != "" {
			table = fmt.Sprintf("%s.%s", msg.Schema(), table)
		}
		op := string(msg.Operation())
		key := fmt.Sprintf("%s:%s", table, op)
		groups[key] = append(groups[key], msg)
	}

	var executor interface {
		Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	}

	if s.tx != nil {
		executor = s.tx
	} else {
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("failed to begin transaction: %w", err)
		}
		defer tx.Rollback(ctx)
		executor = tx
	}

	for key, group := range groups {
		parts := strings.SplitN(key, ":", 2)
		table := parts[0]
		op := hermod.Operation(parts[1])

		for _, msg := range group {
			var err error
			switch op {
			case hermod.OpCreate, hermod.OpSnapshot, hermod.OpUpdate:
				query := fmt.Sprintf(commonQueries[QueryUpsert], table)
				_, err = executor.Exec(ctx, query, msg.ID(), msg.Payload())
			case hermod.OpDelete:
				query := fmt.Sprintf(commonQueries[QueryDelete], table)
				_, err = executor.Exec(ctx, query, msg.ID())
			default:
				err = fmt.Errorf("unsupported operation: %s", op)
			}

			if err != nil {
				return fmt.Errorf("batch write error on message %s: %w", msg.ID(), err)
			}
		}
	}

	if s.tx == nil {
		if tx, ok := executor.(pgx.Tx); ok {
			return tx.Commit(ctx)
		}
	}
	return nil
}

func (s *PostgresSink) init(ctx context.Context) error {
	pool, err := pgxpool.New(ctx, s.connString)
	if err != nil {
		return fmt.Errorf("failed to create postgres pool: %w", err)
	}
	s.pool = pool
	return s.pool.Ping(ctx)
}

func (s *PostgresSink) Begin(ctx context.Context) error {
	if err := s.init(ctx); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	s.tx = tx
	return nil
}

func (s *PostgresSink) Commit(ctx context.Context) error {
	if s.tx == nil {
		return fmt.Errorf("no active transaction")
	}
	err := s.tx.Commit(ctx)
	s.tx = nil
	return err
}

func (s *PostgresSink) Rollback(ctx context.Context) error {
	if s.tx == nil {
		return nil
	}
	err := s.tx.Rollback(ctx)
	s.tx = nil
	return err
}

func (s *PostgresSink) Prepare(ctx context.Context) (string, error) {
	if s.tx == nil {
		return "", fmt.Errorf("no active transaction")
	}
	txID := uuid.New().String()
	_, err := s.tx.Exec(ctx, fmt.Sprintf("PREPARE TRANSACTION '%s'", txID))
	if err != nil {
		return "", err
	}
	s.tx = nil
	return txID, nil
}

func (s *PostgresSink) CommitPrepared(ctx context.Context, txID string) error {
	if err := s.init(ctx); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, fmt.Sprintf("COMMIT PREPARED '%s'", txID))
	return err
}

func (s *PostgresSink) RollbackPrepared(ctx context.Context, txID string) error {
	if err := s.init(ctx); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, fmt.Sprintf("ROLLBACK PREPARED '%s'", txID))
	return err
}

func (s *PostgresSink) Ping(ctx context.Context) error {
	if s.pool == nil {
		if err := s.init(ctx); err != nil {
			return err
		}
	}
	return s.pool.Ping(ctx)
}

func (s *PostgresSink) Close() error {
	if s.pool != nil {
		s.pool.Close()
	}
	return nil
}

func (s *PostgresSink) DiscoverDatabases(ctx context.Context) ([]string, error) {
	if s.pool == nil {
		if err := s.init(ctx); err != nil {
			return nil, err
		}
	}

	rows, err := s.pool.Query(ctx, commonQueries[QueryListDatabases])
	if err != nil {
		return nil, fmt.Errorf("failed to query databases: %w", err)
	}
	defer rows.Close()

	var databases []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		databases = append(databases, name)
	}
	return databases, nil
}

func (s *PostgresSink) DiscoverTables(ctx context.Context) ([]string, error) {
	if s.pool == nil {
		if err := s.init(ctx); err != nil {
			return nil, err
		}
	}

	rows, err := s.pool.Query(ctx, commonQueries[QueryListTables])
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			return nil, err
		}
		tables = append(tables, table)
	}
	return tables, nil
}

func (s *PostgresSink) Sample(ctx context.Context, table string) (hermod.Message, error) {
	msgs, err := s.Browse(ctx, table, 1)
	if err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, fmt.Errorf("no data found in table %s", table)
	}
	return msgs[0], nil
}

func (s *PostgresSink) Browse(ctx context.Context, table string, limit int) ([]hermod.Message, error) {
	if s.pool == nil {
		if err := s.init(ctx); err != nil {
			return nil, err
		}
	}

	quoted, err := sqlutil.QuoteIdent("pgx", table)
	if err != nil {
		return nil, fmt.Errorf("invalid table name: %w", err)
	}
	query := fmt.Sprintf(commonQueries[QueryBrowse], quoted, limit)
	rows, err := s.pool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []hermod.Message
	for rows.Next() {
		fields := rows.FieldDescriptions()
		values, err := rows.Values()
		if err != nil {
			return nil, fmt.Errorf("failed to get values: %w", err)
		}

		record := make(map[string]interface{})
		for i, field := range fields {
			val := values[i]
			if b, ok := val.([]byte); ok {
				record[field.Name] = string(b)
			} else {
				record[field.Name] = val
			}
		}

		afterJSON, _ := json.Marshal(message.SanitizeMap(record))

		msg := message.AcquireMessage()
		msg.SetID(fmt.Sprintf("sample-%s-%d-%d", table, time.Now().Unix(), len(msgs)))
		msg.SetOperation(hermod.OpSnapshot)
		msg.SetTable(table)
		msg.SetAfter(afterJSON)
		msg.SetMetadata("source", "postgres_sink")
		msg.SetMetadata("sample", "true")
		msgs = append(msgs, msg)
	}

	return msgs, nil
}
