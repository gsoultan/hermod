package snowflake

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/snowflakedb/gosnowflake"
	"github.com/user/hermod"
)

// Sink implements the hermod.Sink interface for Snowflake.
type Sink struct {
	db         *sql.DB
	connString string
	formatter  hermod.Formatter
}

func NewSink(connString string, formatter hermod.Formatter) *Sink {
	return &Sink{
		connString: connString,
		formatter:  formatter,
	}
}

func (s *Sink) Write(ctx context.Context, msg hermod.Message) error {
	return s.WriteBatch(ctx, []hermod.Message{msg})
}

func (s *Sink) WriteBatch(ctx context.Context, msgs []hermod.Message) error {
	if len(msgs) == 0 {
		return nil
	}

	if s.db == nil {
		if err := s.init(); err != nil {
			return err
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Prepare statement cache per table for this transaction
	stmts := make(map[string]*sql.Stmt)
	defer func() {
		for _, st := range stmts {
			_ = st.Close()
		}
	}()

	for _, msg := range msgs {
		if msg == nil {
			continue
		}

		table := msg.Table()
		if msg.Schema() != "" {
			table = fmt.Sprintf("%s.%s", msg.Schema(), table)
		}

		payload := msg.Payload()
		if s.formatter != nil {
			formatted, err := s.formatter.Format(msg)
			if err == nil {
				payload = formatted
			}
		}

		// Snowflake MERGE (UPSERT equivalent) — prepare per table
		key := "merge:" + table
		st := stmts[key]
		if st == nil {
			query := fmt.Sprintf(`
                MERGE INTO %s AS target
                USING (SELECT ? AS id, ? AS data) AS source
                ON target.id = source.id
                WHEN MATCHED THEN UPDATE SET target.data = source.data
                WHEN NOT MATCHED THEN INSERT (id, data) VALUES (source.id, source.data)
            `, table)
			st, err = tx.PrepareContext(ctx, query)
			if err != nil {
				return fmt.Errorf("prepare merge failed: %w", err)
			}
			stmts[key] = st
		}

		_, err = st.ExecContext(ctx, msg.ID(), payload)
		if err != nil {
			return fmt.Errorf("failed to execute merge for message %s: %w", msg.ID(), err)
		}
	}

	return tx.Commit()
}

func (s *Sink) init() error {
	db, err := sql.Open("snowflake", s.connString)
	if err != nil {
		return err
	}
	// Conservative pool defaults
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(10)
	db.SetConnMaxIdleTime(60 * time.Second)
	s.db = db
	return s.db.Ping()
}

func (s *Sink) Ping(ctx context.Context) error {
	if s.db == nil {
		if err := s.init(); err != nil {
			return err
		}
	}
	return s.db.PingContext(ctx)
}

func (s *Sink) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}
