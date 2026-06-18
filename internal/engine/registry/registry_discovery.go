package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/factory"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/comm/message"
	"github.com/user/hermod/pkg/infra/sqlutil"
)

// --- Test Connectivity ---

func (r *Registry) TestSource(ctx context.Context, cfg factory.SourceConfig) error {
	src, err := r.createSource(ctx, cfg)
	if err != nil {
		return err
	}
	defer src.Close()

	if readyChecker, ok := src.(hermod.ReadyChecker); ok {
		return readyChecker.IsReady(ctx)
	}
	return src.Ping(ctx)
}

func (r *Registry) TestSink(ctx context.Context, cfg factory.SinkConfig) error {
	if cfg.Type == "stdout" {
		return nil
	}

	_, err := runWithContext(ctx, func() (struct{}, error) {
		snk, err := r.createSink(ctx, cfg)
		if err != nil {
			return struct{}{}, err
		}
		defer snk.Close()
		return struct{}{}, snk.Ping(ctx)
	})
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return fmt.Errorf("sink connection test timed out: %w", err)
	}
	return err
}

// runWithContext executes fn in a separate goroutine and races its completion
// against the context deadline. Some sink/driver implementations may perform
// blocking network operations (e.g. a context-less driver dial) that do not
// honor the provided context. Without this guard such a call could hang far
// beyond the request deadline and surface as an upstream gateway timeout (524).
//
// When the context is cancelled before fn returns, the caller is unblocked
// immediately with the context error. The goroutine is left to finish on its
// own (it cannot be force-stopped); fn is expected to release its own resources
// (e.g. via a deferred Close) once the underlying operation eventually returns.
func runWithContext[T any](ctx context.Context, fn func() (T, error)) (T, error) {
	type result struct {
		val T
		err error
	}
	ch := make(chan result, 1)
	go func() {
		val, err := fn()
		ch <- result{val: val, err: err}
	}()

	select {
	case res := <-ch:
		return res.val, res.err
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
}

// --- Source Discovery ---

func (r *Registry) DiscoverDatabases(ctx context.Context, cfg factory.SourceConfig) ([]string, error) {
	src, err := r.createSource(ctx, cfg)
	if err != nil {
		return nil, err
	}
	defer src.Close()

	if d, ok := src.(hermod.Discoverer); ok {
		return d.DiscoverDatabases(ctx)
	}
	return nil, fmt.Errorf("source type %s does not support database discovery", cfg.Type)
}

func (r *Registry) DiscoverTables(ctx context.Context, cfg factory.SourceConfig) ([]string, error) {
	src, err := r.createSource(ctx, cfg)
	if err != nil {
		return nil, err
	}
	defer src.Close()

	if d, ok := src.(hermod.Discoverer); ok {
		return d.DiscoverTables(ctx)
	}
	return nil, fmt.Errorf("source type %s does not support table discovery", cfg.Type)
}

func (r *Registry) DiscoverSourceColumns(ctx context.Context, cfg factory.SourceConfig, table string) ([]hermod.ColumnInfo, error) {
	src, err := r.createSource(ctx, cfg)
	if err != nil {
		return nil, err
	}
	defer src.Close()

	if d, ok := src.(hermod.ColumnDiscoverer); ok {
		return d.DiscoverColumns(ctx, table)
	}
	return nil, fmt.Errorf("source type %s does not support column discovery", cfg.Type)
}

func (r *Registry) DiscoverReplicationSlots(ctx context.Context, cfg factory.SourceConfig) ([]hermod.ReplicationSlotInfo, error) {
	src, err := r.createSource(ctx, cfg)
	if err != nil {
		return nil, err
	}
	defer src.Close()

	if d, ok := src.(hermod.ReplicationDiscoverer); ok {
		return d.DiscoverReplicationSlots(ctx)
	}
	return nil, fmt.Errorf("source type %s does not support replication slot discovery", cfg.Type)
}

func (r *Registry) DiscoverPublications(ctx context.Context, cfg factory.SourceConfig) ([]hermod.PublicationInfo, error) {
	src, err := r.createSource(ctx, cfg)
	if err != nil {
		return nil, err
	}
	defer src.Close()

	if d, ok := src.(hermod.ReplicationDiscoverer); ok {
		return d.DiscoverPublications(ctx)
	}
	return nil, fmt.Errorf("source type %s does not support publication discovery", cfg.Type)
}

// --- Sink Discovery ---

func (r *Registry) DiscoverSinkDatabases(ctx context.Context, cfg factory.SinkConfig) ([]string, error) {
	return runWithContext(ctx, func() ([]string, error) {
		snk, err := r.createSink(ctx, cfg)
		if err != nil {
			return nil, err
		}
		defer snk.Close()

		if d, ok := snk.(hermod.Discoverer); ok {
			return d.DiscoverDatabases(ctx)
		}
		return nil, fmt.Errorf("sink type %s does not support database discovery", cfg.Type)
	})
}

func (r *Registry) DiscoverSinkTables(ctx context.Context, cfg factory.SinkConfig) ([]string, error) {
	return runWithContext(ctx, func() ([]string, error) {
		snk, err := r.createSink(ctx, cfg)
		if err != nil {
			return nil, err
		}
		defer snk.Close()

		if d, ok := snk.(hermod.Discoverer); ok {
			return d.DiscoverTables(ctx)
		}
		return nil, fmt.Errorf("sink type %s does not support table discovery", cfg.Type)
	})
}

func (r *Registry) DiscoverSinkColumns(ctx context.Context, cfg factory.SinkConfig, table string) ([]hermod.ColumnInfo, error) {
	return runWithContext(ctx, func() ([]hermod.ColumnInfo, error) {
		snk, err := r.createSink(ctx, cfg)
		if err != nil {
			return nil, err
		}
		defer snk.Close()

		if d, ok := snk.(hermod.ColumnDiscoverer); ok {
			return d.DiscoverColumns(ctx, table)
		}
		return nil, fmt.Errorf("sink type %s does not support column discovery", cfg.Type)
	})
}

// --- Sampling & Browsing ---

func (r *Registry) SampleTable(ctx context.Context, cfg factory.SourceConfig, table string) (hermod.Message, error) {
	src, err := r.createSource(ctx, cfg)
	if err != nil {
		return nil, err
	}
	defer src.Close()

	if s, ok := src.(hermod.Sampler); ok {
		return s.Sample(ctx, table)
	}
	return nil, fmt.Errorf("source type %s does not support sampling", cfg.Type)
}

func (r *Registry) SampleSinkTable(ctx context.Context, cfg factory.SinkConfig, table string) (hermod.Message, error) {
	msgs, err := r.BrowseSinkTable(ctx, cfg, table, 1)
	if err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, fmt.Errorf("no data found in sink table %s", table)
	}
	return msgs[0], nil
}

func (r *Registry) BrowseSinkTable(ctx context.Context, cfg factory.SinkConfig, table string, limit int) ([]hermod.Message, error) {
	snk, err := r.createSink(ctx, cfg)
	if err != nil {
		return nil, err
	}
	defer snk.Close()

	if b, ok := snk.(hermod.Browser); ok {
		msgs, err := b.Browse(ctx, table, limit)
		if err == nil {
			return msgs, nil
		}
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

func (r *Registry) ExecuteSQL(ctx context.Context, cfg factory.SourceConfig, query string) ([]map[string]any, error) {
	// 1. Try if the source already implements SQLExecutor
	src, err := r.createSource(ctx, cfg)
	if err == nil {
		defer src.Close()
		if e, ok := src.(hermod.SQLExecutor); ok {
			results, err := e.ExecuteSQL(ctx, query)
			if err == nil {
				return results, nil
			}
			// If it's not supported, fallback to generic execution.
			// Otherwise, return the error (e.g. SQL syntax error).
			if !errors.Is(err, hermod.ErrNotSupported) {
				return nil, err
			}
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

	return sqlutil.ScanRows(rows)
}

func (r *Registry) ExecuteSinkSQL(ctx context.Context, cfg factory.SinkConfig, query string) ([]map[string]any, error) {
	// 1. Try if the sink already implements SQLExecutor
	snk, err := r.createSink(ctx, cfg)
	if err == nil {
		defer snk.Close()
		if e, ok := snk.(hermod.SQLExecutor); ok {
			results, err := e.ExecuteSQL(ctx, query)
			if err == nil {
				return results, nil
			}
			if !errors.Is(err, hermod.ErrNotSupported) {
				return nil, err
			}
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

	return sqlutil.ScanRows(rows)
}

// ExecSinkStatement executes a non-query SQL statement (DDL/DML) against the sink.
// It is intended for operations like TRUNCATE, ALTER, or DELETE that do not return rows.
func (r *Registry) ExecSinkStatement(ctx context.Context, cfg factory.SinkConfig, stmt string) error {
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

func (r *Registry) GetSourceFormSamples(ctx context.Context, path string, limit int) ([]hermod.Message, error) {
	if r.storage == nil {
		return nil, nil
	}
	subs, _, err := r.storage.ListFormSubmissions(ctx, storage.FormSubmissionFilter{
		CommonFilter: storage.CommonFilter{Limit: limit},
		Path:         path,
	})
	if err != nil {
		return nil, err
	}

	var msgs []hermod.Message
	for _, s := range subs {
		msg := message.AcquireMessage()
		if err := json.Unmarshal(s.Data, &msg); err == nil {
			msgs = append(msgs, msg)
		} else {
			msg.SetData("raw", string(s.Data))
			msgs = append(msgs, msg)
		}
	}
	return msgs, nil
}
