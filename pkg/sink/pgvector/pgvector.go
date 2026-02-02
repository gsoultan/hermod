package pgvector

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/user/hermod"
)

func init() {
	// Register will be handled in a factory or explicitly in main.go
}

// Sink implements the hermod.Sink interface for pgvector.
type Sink struct {
	pool           *pgxpool.Pool
	connString     string
	table          string
	vectorColumn   string
	idColumn       string
	metadataColumn string
}

func NewSink(connString, table, vectorCol, idCol, metadataCol string) *Sink {
	return &Sink{
		connString:     connString,
		table:          table,
		vectorColumn:   vectorCol,
		idColumn:       idCol,
		metadataColumn: metadataCol,
	}
}

func (s *Sink) Write(ctx context.Context, msg hermod.Message) error {
	return s.WriteBatch(ctx, []hermod.Message{msg})
}

func (s *Sink) WriteBatch(ctx context.Context, msgs []hermod.Message) error {
	if len(msgs) == 0 {
		return nil
	}
	if s.pool == nil {
		if err := s.init(ctx); err != nil {
			return err
		}
	}

	for _, msg := range msgs {
		data := msg.Data()
		vector, ok := data[s.vectorColumn]
		if !ok {
			return fmt.Errorf("vector column %s not found in message", s.vectorColumn)
		}

		// Convert vector to postgres format [1,2,3]
		vectorStr := formatVector(vector)
		if vectorStr == "" {
			return fmt.Errorf("invalid vector format for column %s", s.vectorColumn)
		}

		id := msg.ID()
		if s.idColumn != "" {
			if val, ok := data[s.idColumn]; ok {
				id = fmt.Sprintf("%v", val)
			}
		}

		query := fmt.Sprintf("INSERT INTO %s (%s, %s", s.table, s.idColumn, s.vectorColumn)
		placeholders := "$1, $2"
		args := []interface{}{id, vectorStr}

		if s.metadataColumn != "" {
			query += ", " + s.metadataColumn
			placeholders += ", $3"
			args = append(args, data) // Use full data as metadata
		}
		query += fmt.Sprintf(") VALUES (%s) ON CONFLICT (%s) DO UPDATE SET %s = $2", placeholders, s.idColumn, s.vectorColumn)
		if s.metadataColumn != "" {
			query += fmt.Sprintf(", %s = $3", s.metadataColumn)
		}

		_, err := s.pool.Exec(ctx, query, args...)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Sink) init(ctx context.Context) error {
	pool, err := pgxpool.New(ctx, s.connString)
	if err != nil {
		return err
	}
	s.pool = pool
	return s.pool.Ping(ctx)
}

func (s *Sink) Ping(ctx context.Context) error {
	if s.pool == nil {
		if err := s.init(ctx); err != nil {
			return err
		}
	}
	return s.pool.Ping(ctx)
}

func (s *Sink) Close() error {
	if s.pool != nil {
		s.pool.Close()
	}
	return nil
}

func formatVector(v interface{}) string {
	switch val := v.(type) {
	case []float32:
		return formatFloat32(val)
	case []float64:
		return formatFloat64(val)
	case []interface{}:
		var parts []string
		for _, x := range val {
			parts = append(parts, fmt.Sprintf("%v", x))
		}
		return "[" + strings.Join(parts, ",") + "]"
	default:
		return ""
	}
}

func formatFloat32(v []float32) string {
	var parts []string
	for _, x := range v {
		parts = append(parts, fmt.Sprintf("%g", x))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func formatFloat64(v []float64) string {
	var parts []string
	for _, x := range v {
		parts = append(parts, fmt.Sprintf("%g", x))
	}
	return "[" + strings.Join(parts, ",") + "]"
}
