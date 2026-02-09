package pgvector

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/evaluator"
	"github.com/user/hermod/pkg/sqlutil"
)

func init() {
	// Register will be handled in a factory or explicitly in main.go
}

// Sink implements the hermod.Sink interface for pgvector.
type Sink struct {
	pool             *pgxpool.Pool
	connString       string
	table            string
	vectorColumn     string
	idColumn         string
	metadataColumn   string
	mappings         []sqlutil.ColumnMapping
	useExistingTable bool
	deleteStrategy   string
	softDeleteColumn string
	softDeleteValue  string
	verifiedTables   sync.Map
}

func NewSink(connString, table, vectorCol, idCol, metadataCol string, mappings []sqlutil.ColumnMapping, useExistingTable bool, deleteStrategy string, softDeleteColumn string, softDeleteValue string) *Sink {
	return &Sink{
		connString:       connString,
		table:            table,
		vectorColumn:     vectorCol,
		idColumn:         idCol,
		metadataColumn:   metadataCol,
		mappings:         mappings,
		useExistingTable: useExistingTable,
		deleteStrategy:   deleteStrategy,
		softDeleteColumn: softDeleteColumn,
		softDeleteValue:  softDeleteValue,
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

	// Ensure table exists
	if err := s.ensureTable(ctx, s.table); err != nil {
		return fmt.Errorf("ensure table %s: %w", s.table, err)
	}

	for _, msg := range msgs {
		if msg == nil {
			continue
		}

		op := msg.Operation()
		if op == "" {
			op = hermod.OpCreate
		}

		if op == hermod.OpDelete {
			if s.deleteStrategy == "ignore" {
				continue
			}
			if len(s.mappings) > 0 {
				if err := s.deleteMapped(ctx, msg); err != nil {
					return err
				}
			} else {
				idCol := "id"
				if s.idColumn != "" {
					idCol = s.idColumn
				}
				query := fmt.Sprintf("DELETE FROM %s WHERE %s = $1", s.table, idCol)
				_, err := s.pool.Exec(ctx, query, msg.ID())
				if err != nil {
					return fmt.Errorf("pgvector delete error: %w", err)
				}
			}
			continue
		}

		if len(s.mappings) > 0 {
			if err := s.upsertMapped(ctx, msg); err != nil {
				return err
			}
			continue
		}

		data := msg.Data()
		if data == nil {
			_ = json.Unmarshal(msg.Payload(), &data)
		}
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

func (s *Sink) upsertMapped(ctx context.Context, msg hermod.Message) error {
	data := msg.Data()
	if data == nil {
		_ = json.Unmarshal(msg.Payload(), &data)
	}

	var cols []string
	var placeholders []string
	var args []any
	var updateParts []string
	var pkCol string

	for _, m := range s.mappings {
		val := evaluator.GetMsgValByPath(msg, m.SourceField)

		if m.IsIdentity && (val == nil || val == "" || val == 0) {
			continue
		}

		// Special handling for vector column in mappings
		if strings.EqualFold(m.TargetColumn, s.vectorColumn) || strings.Contains(strings.ToLower(m.DataType), "vector") {
			val = formatVector(val)
		}

		quoted, _ := sqlutil.QuoteIdent("pgx", m.TargetColumn)
		cols = append(cols, quoted)
		placeholder := fmt.Sprintf("$%d", len(args)+1)
		placeholders = append(placeholders, placeholder)
		args = append(args, val)

		if m.IsPrimaryKey {
			pkCol = quoted
		} else {
			updateParts = append(updateParts, fmt.Sprintf("%s = %s", quoted, placeholder))
		}
	}

	if pkCol == "" {
		pkCol = cols[0] // Fallback
	}

	query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) ON CONFLICT (%s) DO UPDATE SET %s",
		s.table, strings.Join(cols, ", "), strings.Join(placeholders, ", "), pkCol, strings.Join(updateParts, ", "))

	_, err := s.pool.Exec(ctx, query, args...)
	return err
}

func (s *Sink) deleteMapped(ctx context.Context, msg hermod.Message) error {
	data := msg.Data()
	if data == nil {
		if len(msg.Before()) > 0 {
			_ = json.Unmarshal(msg.Before(), &data)
		} else if len(msg.Payload()) > 0 {
			_ = json.Unmarshal(msg.Payload(), &data)
		}
	}

	var pks []string
	var args []any

	for _, m := range s.mappings {
		if m.IsPrimaryKey {
			val := evaluator.GetMsgValByPath(msg, m.SourceField)
			qCol, _ := sqlutil.QuoteIdent("pgx", m.TargetColumn)
			pks = append(pks, fmt.Sprintf("%s = $%d", qCol, len(args)+1))
			args = append(args, val)
		}
	}

	if len(pks) == 0 {
		idCol := "id"
		if s.idColumn != "" {
			idCol = s.idColumn
		}
		query := fmt.Sprintf("DELETE FROM %s WHERE %s = $1", s.table, idCol)
		_, err := s.pool.Exec(ctx, query, msg.ID())
		return err
	}

	if s.deleteStrategy == "soft_delete" && s.softDeleteColumn != "" {
		qCol, _ := sqlutil.QuoteIdent("pgx", s.softDeleteColumn)
		query := fmt.Sprintf("UPDATE %s SET %s = $%d WHERE %s",
			s.table, qCol, len(args)+1, strings.Join(pks, " AND "))
		args = append(args, s.softDeleteValue)
		_, err := s.pool.Exec(ctx, query, args...)
		return err
	}

	query := fmt.Sprintf("DELETE FROM %s WHERE %s", s.table, strings.Join(pks, " AND "))
	_, err := s.pool.Exec(ctx, query, args...)
	return err
}

func (s *Sink) ensureTable(ctx context.Context, table string) error {
	if _, ok := s.verifiedTables.Load(table); ok {
		return nil
	}

	if s.useExistingTable {
		s.verifiedTables.Store(table, true)
		return nil
	}

	// Create table logic similar to PostgresSink but with vector support
	var query string
	if len(s.mappings) > 0 {
		var cols []string
		for _, m := range s.mappings {
			dataType := m.DataType
			if dataType == "" {
				dataType = "TEXT"
			}
			quoted, _ := sqlutil.QuoteIdent("pgx", m.TargetColumn)
			colDef := fmt.Sprintf("%s %s", quoted, dataType)
			if m.IsIdentity {
				if strings.ToUpper(dataType) == "UUID" {
					colDef += " DEFAULT gen_random_uuid()"
				} else if strings.Contains(strings.ToUpper(dataType), "INT") {
					if strings.Contains(strings.ToUpper(dataType), "BIG") {
						dataType = "BIGSERIAL"
					} else {
						dataType = "SERIAL"
					}
					colDef = fmt.Sprintf("%s %s", quoted, dataType)
				}
			}
			if m.IsPrimaryKey {
				colDef += " PRIMARY KEY"
			} else if !m.IsNullable {
				colDef += " NOT NULL"
			}
			cols = append(cols, colDef)
		}
		query = fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (%s)", table, strings.Join(cols, ", "))
	} else {
		// Default schema
		idCol := "id"
		if s.idColumn != "" {
			idCol = s.idColumn
		}
		vecCol := "embedding"
		if s.vectorColumn != "" {
			vecCol = s.vectorColumn
		}
		metaCol := "metadata"
		if s.metadataColumn != "" {
			metaCol = s.metadataColumn
		}
		query = fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (%s TEXT PRIMARY KEY, %s vector, %s JSONB)",
			table, idCol, vecCol, metaCol)
	}

	// Ensure pgvector extension exists
	_, _ = s.pool.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS vector")

	_, err := s.pool.Exec(ctx, query)
	if err == nil {
		s.verifiedTables.Store(table, true)
	}
	return err
}

func (s *Sink) DiscoverColumns(ctx context.Context, table string) ([]hermod.ColumnInfo, error) {
	if s.pool == nil {
		if err := s.init(ctx); err != nil {
			return nil, err
		}
	}

	query := `
		SELECT column_name, data_type, is_nullable = 'YES', 
		       EXISTS (
		           SELECT 1 FROM information_schema.key_column_usage kcu
		           JOIN information_schema.table_constraints tc ON kcu.constraint_name = tc.constraint_name
		           WHERE kcu.table_name = $1 AND tc.constraint_type = 'PRIMARY KEY' AND kcu.column_name = columns.column_name
		       ) as is_pk,
		       column_default
		FROM information_schema.columns
		WHERE table_name = $1 OR table_schema || '.' || table_name = $1
		ORDER BY ordinal_position`

	rows, err := s.pool.Query(ctx, query, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var columns []hermod.ColumnInfo
	for rows.Next() {
		var col hermod.ColumnInfo
		var def *string
		if err := rows.Scan(&col.Name, &col.Type, &col.IsNullable, &col.IsPK, &def); err != nil {
			return nil, err
		}
		if def != nil {
			col.Default = *def
			col.IsIdentity = strings.Contains(strings.ToLower(*def), "nextval")
		}
		columns = append(columns, col)
	}
	return columns, nil
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
