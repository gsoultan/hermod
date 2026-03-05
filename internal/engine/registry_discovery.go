package engine

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/storage"
)

// --- Test Connectivity ---

func (r *Registry) TestSource(ctx context.Context, cfg SourceConfig) error {
	src, err := r.createSource(cfg)
	if err != nil {
		return err
	}
	defer src.Close()

	if readyChecker, ok := src.(hermod.ReadyChecker); ok {
		return readyChecker.IsReady(ctx)
	}
	return src.Ping(ctx)
}

func (r *Registry) TestSink(ctx context.Context, cfg SinkConfig) error {
	if cfg.Type == "stdout" {
		return nil
	}
	snk, err := r.createSink(cfg)
	if err != nil {
		return err
	}
	defer snk.Close()
	return snk.Ping(ctx)
}

// --- Source Discovery ---

func (r *Registry) DiscoverDatabases(ctx context.Context, cfg SourceConfig) ([]string, error) {
	src, err := r.createSource(cfg)
	if err != nil {
		return nil, err
	}
	defer src.Close()

	if d, ok := src.(hermod.Discoverer); ok {
		return d.DiscoverDatabases(ctx)
	}
	return nil, fmt.Errorf("source type %s does not support database discovery", cfg.Type)
}

func (r *Registry) DiscoverTables(ctx context.Context, cfg SourceConfig) ([]string, error) {
	src, err := r.createSource(cfg)
	if err != nil {
		return nil, err
	}
	defer src.Close()

	if d, ok := src.(hermod.Discoverer); ok {
		return d.DiscoverTables(ctx)
	}
	return nil, fmt.Errorf("source type %s does not support table discovery", cfg.Type)
}

func (r *Registry) DiscoverSourceColumns(ctx context.Context, cfg SourceConfig, table string) ([]hermod.ColumnInfo, error) {
	src, err := r.createSource(cfg)
	if err != nil {
		return nil, err
	}
	defer src.Close()

	if d, ok := src.(hermod.ColumnDiscoverer); ok {
		return d.DiscoverColumns(ctx, table)
	}
	return nil, fmt.Errorf("source type %s does not support column discovery", cfg.Type)
}

// --- Sink Discovery ---

func (r *Registry) DiscoverSinkDatabases(ctx context.Context, cfg SinkConfig) ([]string, error) {
	snk, err := r.createSink(cfg)
	if err != nil {
		return nil, err
	}
	defer snk.Close()

	if d, ok := snk.(hermod.Discoverer); ok {
		return d.DiscoverDatabases(ctx)
	}
	return nil, fmt.Errorf("sink type %s does not support database discovery", cfg.Type)
}

func (r *Registry) DiscoverSinkTables(ctx context.Context, cfg SinkConfig) ([]string, error) {
	snk, err := r.createSink(cfg)
	if err != nil {
		return nil, err
	}
	defer snk.Close()

	if d, ok := snk.(hermod.Discoverer); ok {
		return d.DiscoverTables(ctx)
	}
	return nil, fmt.Errorf("sink type %s does not support table discovery", cfg.Type)
}

func (r *Registry) DiscoverSinkColumns(ctx context.Context, cfg SinkConfig, table string) ([]hermod.ColumnInfo, error) {
	snk, err := r.createSink(cfg)
	if err != nil {
		return nil, err
	}
	defer snk.Close()

	if d, ok := snk.(hermod.ColumnDiscoverer); ok {
		return d.DiscoverColumns(ctx, table)
	}
	return nil, fmt.Errorf("sink type %s does not support column discovery", cfg.Type)
}

// --- Sampling & Browsing ---

func (r *Registry) SampleTable(ctx context.Context, cfg SourceConfig, table string) (hermod.Message, error) {
	src, err := r.createSource(cfg)
	if err != nil {
		return nil, err
	}
	defer src.Close()

	if s, ok := src.(hermod.Sampler); ok {
		return s.Sample(ctx, table)
	}
	return nil, fmt.Errorf("source type %s does not support sampling", cfg.Type)
}

func (r *Registry) SampleSinkTable(ctx context.Context, cfg SinkConfig, table string) (hermod.Message, error) {
	msgs, err := r.BrowseSinkTable(ctx, cfg, table, 1)
	if err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, fmt.Errorf("no data found in sink table %s", table)
	}
	return msgs[0], nil
}

func (r *Registry) BrowseSinkTable(ctx context.Context, cfg SinkConfig, table string, limit int) ([]hermod.Message, error) {
	snk, err := r.createSink(cfg)
	if err != nil {
		return nil, err
	}
	defer snk.Close()

	if b, ok := snk.(hermod.Browser); ok {
		return b.Browse(ctx, table, limit)
	}

	if s, ok := snk.(hermod.Sampler); ok && limit == 1 {
		msg, err := s.Sample(ctx, table)
		if err != nil {
			return nil, err
		}
		return []hermod.Message{msg}, nil
	}

	return nil, fmt.Errorf("sink type %s does not support browsing", cfg.Type)
}

// --- SQL Execution ---

func (r *Registry) ExecuteSQL(ctx context.Context, cfg SourceConfig, query string) ([]map[string]any, error) {
	// 1. Try if the source already implements SQLExecutor
	src, err := r.createSource(cfg)
	if err == nil {
		defer src.Close()
		if e, ok := src.(hermod.SQLExecutor); ok {
			return e.ExecuteSQL(ctx, query)
		}
	}

	// 2. Fallback to generic SQL execution if it's a supported DB type
	db, err := r.getOrOpenDB(storage.Source{
		ID:     "temp-query-" + cfg.Type,
		Type:   cfg.Type,
		Config: cfg.Config,
	})
	if err != nil {
		return nil, err
	}

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanRows(rows)
}

func (r *Registry) ExecuteSinkSQL(ctx context.Context, cfg SinkConfig, query string) ([]map[string]any, error) {
	// 1. Try if the sink already implements SQLExecutor
	snk, err := r.createSink(cfg)
	if err == nil {
		defer snk.Close()
		if e, ok := snk.(hermod.SQLExecutor); ok {
			return e.ExecuteSQL(ctx, query)
		}
	}

	// 2. Fallback to generic SQL execution if it's a supported DB type
	db, err := r.getOrOpenDB(storage.Source{
		ID:     "temp-sink-query-" + cfg.Type,
		Type:   cfg.Type,
		Config: cfg.Config,
	})
	if err != nil {
		return nil, err
	}

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanRows(rows)
}

// ExecSinkStatement executes a non-query SQL statement (DDL/DML) against the sink.
// It is intended for operations like TRUNCATE, ALTER, or DELETE that do not return rows.
func (r *Registry) ExecSinkStatement(ctx context.Context, cfg SinkConfig, stmt string) error {
	// Prefer using a direct DB connection for supported SQL drivers
	db, err := r.getOrOpenDB(storage.Source{
		ID:     "temp-sink-exec-" + cfg.Type,
		Type:   cfg.Type,
		Config: cfg.Config,
	})
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, stmt)
	return err
}

func scanRows(rows *sql.Rows) ([]map[string]any, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	var results []map[string]any
	for rows.Next() {
		columns := make([]any, len(cols))
		columnPointers := make([]any, len(cols))
		for i := range columns {
			columnPointers[i] = &columns[i]
		}

		if err := rows.Scan(columnPointers...); err != nil {
			return nil, err
		}

		m := make(map[string]any)
		for i, colName := range cols {
			val := columns[i]
			if b, ok := val.([]byte); ok {
				m[colName] = string(b)
			} else {
				m[colName] = val
			}
		}
		results = append(results, m)
	}
	return results, nil
}
