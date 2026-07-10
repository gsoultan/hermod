package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/factory"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/comm/message"
	"github.com/user/hermod/pkg/comm/transformer/core"
	"github.com/user/hermod/pkg/infra/sqlutil"
)

// errOperationPanicked is returned when a source/sink connectivity or discovery
// operation panics. It allows callers to recognise (via errors.Is) that the
// failure originated from a recovered panic rather than a regular error.
var errOperationPanicked = errors.New("operation panicked")

// --- Test Connectivity ---

func (r *Registry) discoveryKey(prefix string, cfg any) string {
	b, _ := json.Marshal(cfg)
	return prefix + ":" + string(b)
}

func (r *Registry) TestSource(ctx context.Context, cfg factory.SourceConfig) error {
	key := r.discoveryKey("test-source", cfg)
	_, err := r.discoveryDo(ctx, key, func(ctx context.Context) (any, error) {
		src, err := r.createSource(ctx, cfg)
		if err != nil {
			return struct{}{}, err
		}
		if src == nil {
			return struct{}{}, fmt.Errorf("source type %q produced a nil source", cfg.Type)
		}
		defer src.Close()

		if readyChecker, ok := src.(hermod.ReadyChecker); ok {
			return struct{}{}, readyChecker.IsReady(ctx)
		}
		return struct{}{}, src.Ping(ctx)
	})
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return fmt.Errorf("source connection test timed out: %w", err)
	}
	return err
}

func (r *Registry) TestSink(ctx context.Context, cfg factory.SinkConfig) error {
	if cfg.Type == "stdout" {
		return nil
	}

	key := r.discoveryKey("test-sink", cfg)
	_, err := r.discoveryDo(ctx, key, func(ctx context.Context) (any, error) {
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
		// Recover from any panic inside fn (e.g. a database driver dereferencing
		// a nil pointer on a malformed DSN, or a source constructor panicking on
		// invalid config). Without this guard the panic would propagate to the
		// top of this goroutine and crash the entire process — which also hosts
		// the API server — surfacing to clients as an upstream 520. Converting
		// the panic into an error keeps the server alive and lets the caller
		// report a clean failure to the user.
		defer func() {
			if rec := recover(); rec != nil {
				var zero T
				// Ensure we don't send to a closed channel or block forever
				select {
				case ch <- result{
					val: zero,
					err: fmt.Errorf("%w: %v\n%s", errOperationPanicked, rec, debug.Stack()),
				}:
				default:
				}
			}
		}()
		val, err := fn()
		select {
		case ch <- result{val: val, err: err}:
		default:
		}
	}()

	select {
	case res := <-ch:
		return res.val, res.err
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
}

// discoveryDo executes fn through singleflight, ensuring that multiple concurrent
// requests for the same key share the same result. It wraps the execution in
// runWithContext using a background timeout so that the shared task is not
// cancelled if a single caller's context is cancelled. This prevents the
// "cascading cancellation" bug where the first caller's abort fails all
// subsequent concurrent callers.
func (r *Registry) discoveryDo(ctx context.Context, key string, fn func(ctx context.Context) (any, error)) (any, error) {
	ch := r.sf.DoChan(key, func() (any, error) {
		workCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return runWithContext(workCtx, func() (any, error) {
			return fn(workCtx)
		})
	})

	select {
	case res := <-ch:
		return res.Val, res.Err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// --- Source Discovery ---

func (r *Registry) DiscoverDatabases(ctx context.Context, cfg factory.SourceConfig) ([]string, error) {
	key := r.discoveryKey("discover-dbs", cfg)
	val, err := r.discoveryDo(ctx, key, func(ctx context.Context) (any, error) {
		src, err := r.createSource(ctx, cfg)
		if err != nil {
			return nil, err
		}
		defer src.Close()

		if d, ok := src.(hermod.Discoverer); ok {
			return d.DiscoverDatabases(ctx)
		}
		return nil, fmt.Errorf("source type %s does not support database discovery", cfg.Type)
	})
	if err != nil {
		return nil, err
	}
	return val.([]string), nil
}

func (r *Registry) DiscoverTables(ctx context.Context, cfg factory.SourceConfig) ([]string, error) {
	key := r.discoveryKey("discover-tables", cfg)
	val, err := r.discoveryDo(ctx, key, func(ctx context.Context) (any, error) {
		src, err := r.createSource(ctx, cfg)
		if err != nil {
			return nil, err
		}
		defer src.Close()

		if d, ok := src.(hermod.Discoverer); ok {
			return d.DiscoverTables(ctx)
		}
		return nil, fmt.Errorf("source type %s does not support table discovery", cfg.Type)
	})
	if err != nil {
		return nil, err
	}
	return val.([]string), nil
}

func (r *Registry) DiscoverSourceColumns(ctx context.Context, cfg factory.SourceConfig, table string) ([]hermod.ColumnInfo, error) {
	key := r.discoveryKey("discover-columns", struct {
		Cfg   factory.SourceConfig
		Table string
	}{cfg, table})
	val, err := r.discoveryDo(ctx, key, func(ctx context.Context) (any, error) {
		src, err := r.createSource(ctx, cfg)
		if err != nil {
			return nil, err
		}
		defer src.Close()

		if d, ok := src.(hermod.ColumnDiscoverer); ok {
			return d.DiscoverColumns(ctx, table)
		}
		return nil, fmt.Errorf("source type %s does not support column discovery", cfg.Type)
	})
	if err != nil {
		return nil, err
	}
	return val.([]hermod.ColumnInfo), nil
}

func (r *Registry) DiscoverReplicationSlots(ctx context.Context, cfg factory.SourceConfig) ([]hermod.ReplicationSlotInfo, error) {
	key := r.discoveryKey("discover-slots", cfg)
	val, err := r.discoveryDo(ctx, key, func(ctx context.Context) (any, error) {
		src, err := r.createSource(ctx, cfg)
		if err != nil {
			return nil, err
		}
		defer src.Close()

		if d, ok := src.(hermod.ReplicationDiscoverer); ok {
			return d.DiscoverReplicationSlots(ctx)
		}
		return nil, fmt.Errorf("source type %s does not support replication slot discovery", cfg.Type)
	})
	if err != nil {
		return nil, err
	}
	return val.([]hermod.ReplicationSlotInfo), nil
}

func (r *Registry) DiscoverPublications(ctx context.Context, cfg factory.SourceConfig) ([]hermod.PublicationInfo, error) {
	key := r.discoveryKey("discover-pubs", cfg)
	val, err := r.discoveryDo(ctx, key, func(ctx context.Context) (any, error) {
		src, err := r.createSource(ctx, cfg)
		if err != nil {
			return nil, err
		}
		defer src.Close()

		if d, ok := src.(hermod.ReplicationDiscoverer); ok {
			return d.DiscoverPublications(ctx)
		}
		return nil, fmt.Errorf("source type %s does not support publication discovery", cfg.Type)
	})
	if err != nil {
		return nil, err
	}
	return val.([]hermod.PublicationInfo), nil
}

// --- Sink Discovery ---

func (r *Registry) DiscoverSinkDatabases(ctx context.Context, cfg factory.SinkConfig) ([]string, error) {
	key := r.discoveryKey("discover-sink-dbs", cfg)
	val, err := r.discoveryDo(ctx, key, func(ctx context.Context) (any, error) {
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
	if err != nil {
		return nil, err
	}
	return val.([]string), nil
}

func (r *Registry) DiscoverSinkTables(ctx context.Context, cfg factory.SinkConfig) ([]string, error) {
	key := r.discoveryKey("discover-sink-tables", cfg)
	val, err := r.discoveryDo(ctx, key, func(ctx context.Context) (any, error) {
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
	if err != nil {
		return nil, err
	}
	return val.([]string), nil
}

func (r *Registry) DiscoverSinkColumns(ctx context.Context, cfg factory.SinkConfig, table string) ([]hermod.ColumnInfo, error) {
	key := r.discoveryKey("discover-sink-columns", struct {
		Cfg   factory.SinkConfig
		Table string
	}{cfg, table})
	val, err := r.discoveryDo(ctx, key, func(ctx context.Context) (any, error) {
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
	if err != nil {
		return nil, err
	}
	return val.([]hermod.ColumnInfo), nil
}

// --- Sampling & Browsing ---

func (r *Registry) SampleTable(ctx context.Context, cfg factory.SourceConfig, table string) (hermod.Message, error) {
	key := r.discoveryKey("sample-source", struct {
		Cfg   factory.SourceConfig
		Table string
	}{cfg, table})
	val, err := r.discoveryDo(ctx, key, func(ctx context.Context) (any, error) {
		return r.sampleTable(ctx, cfg, table)
	})
	if err != nil {
		return nil, err
	}
	if val == nil {
		return nil, nil
	}
	return val.(hermod.Message), nil
}

func (r *Registry) sampleTable(ctx context.Context, cfg factory.SourceConfig, table string) (hermod.Message, error) {
	src, err := r.createSource(ctx, cfg)
	if err != nil {
		// The source cannot even be created right now (e.g. it is exclusively
		// held by the running workflow). Fall back to the latest record the
		// source actually delivered downstream, if any.
		if msg, ok := loadDeliveredSample(cfg.ID); ok {
			return msg, nil
		}
		return nil, err
	}
	defer src.Close()

	s, ok := src.(hermod.Sampler)
	if !ok {
		// The source type has no native sampling support, but it may still be
		// actively delivering data in a running workflow.
		if msg, ok := loadDeliveredSample(cfg.ID); ok {
			return msg, nil
		}
		return nil, fmt.Errorf("source type %s does not support sampling", cfg.Type)
	}

	msg, err := s.Sample(ctx, table)
	if err == nil && hasSampleData(msg) {
		return msg, nil
	}

	// A passive Sample came back empty (or errored). This is the common case
	// for streaming sources whose live workflow consumer drains every
	// available record. Surface the latest delivered record instead so the
	// sample is never empty while data is flowing.
	if fallback, ok := loadDeliveredSample(cfg.ID); ok {
		return fallback, nil
	}
	if err != nil {
		return nil, err
	}
	return msg, nil
}

// hasSampleData reports whether a sampled message carries any usable content
// (decoded data fields or a raw payload/before/after body).
func hasSampleData(msg hermod.Message) bool {
	if msg == nil {
		return false
	}
	if len(msg.Data()) > 0 {
		return true
	}
	return len(msg.Payload()) > 0 || len(msg.After()) > 0 || len(msg.Before()) > 0
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
	key := r.discoveryKey("browse-sink", struct {
		Cfg   factory.SinkConfig
		Table string
		Limit int
	}{cfg, table, limit})
	val, err := r.discoveryDo(ctx, key, func(ctx context.Context) (any, error) {
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
	})
	if err != nil {
		return nil, err
	}
	return val.([]hermod.Message), nil
}

// --- SQL Execution ---

func (r *Registry) ExecuteSQL(ctx context.Context, cfg factory.SourceConfig, query string, userSample map[string]any) ([]map[string]any, error) {
	key := r.discoveryKey("exec-sql", struct {
		Cfg    factory.SourceConfig
		Query  string
		Sample map[string]any
	}{cfg, query, userSample})
	val, err := r.discoveryDo(ctx, key, func(ctx context.Context) (any, error) {
		// Start with default fallback
		sampleData := map[string]any{
			"after": map[string]any{"id": "sample-id"},
		}

		// Use user-provided sample data if available (highest priority)
		if len(userSample) > 0 {
			for k, v := range userSample {
				sampleData[k] = v
			}
		} else {
			// Fallback to table sampling if no user sample provided
			if msg, err := r.sampleTable(ctx, cfg, ""); err == nil && msg != nil {
				data := msg.Data()
				if len(data) > 0 {
					for k, v := range data {
						sampleData[k] = v
					}
				}
				// If data is empty but we have After payload, try to unmarshal it
				if len(data) == 0 {
					if after := msg.After(); len(after) > 0 {
						var afterData map[string]any
						if err := json.Unmarshal(after, &afterData); err == nil {
							sampleData["after"] = afterData
						}
					}
				}
			}
		}

		// Determine driver for correct placeholder style (?, $1, etc.)
		driver := cfg.Type
		if driver == "batch_sql" {
			if underlyingID := cfg.Config["source_id"]; underlyingID != "" {
				if underlying, err := r.GetSourceConfig(ctx, underlyingID); err == nil {
					driver = underlying.Type
				}
			}
		}

		// Resolve templates safely
		parameterizedQuery, args := core.ParameterizeTemplate(driver, query, sampleData)

		// 1. Try if the source already implements SQLExecutor
		// Only if there are no arguments, as SQLExecutor.ExecuteSQL doesn't support them.
		if len(args) == 0 {
			src, err := r.createSource(ctx, cfg)
			if err == nil {
				defer src.Close()
				if e, ok := src.(hermod.SQLExecutor); ok {
					results, err := e.ExecuteSQL(ctx, parameterizedQuery)
					if err == nil {
						return results, nil
					}
					if !errors.Is(err, hermod.ErrNotSupported) {
						return nil, err
					}
				}
			}
		}

		// 2. Fallback to generic SQL execution if it's a supported DB type
		db, err := r.getOrOpenDB(storage.Source{
			ID:     "temp-query-" + cfg.Type + "-" + cfg.ID,
			Type:   cfg.Type,
			Config: cfg.Config,
		})
		if err != nil {
			return nil, err
		}

		rows, err := db.QueryContext(ctx, parameterizedQuery, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		return sqlutil.ScanRows(rows)
	})
	if err != nil {
		return nil, err
	}
	return val.([]map[string]any), nil
}

func (r *Registry) ExecuteSinkSQL(ctx context.Context, cfg factory.SinkConfig, query string, userSample map[string]any) ([]map[string]any, error) {
	key := r.discoveryKey("exec-sink-sql", struct {
		Cfg    factory.SinkConfig
		Query  string
		Sample map[string]any
	}{cfg, query, userSample})
	val, err := r.discoveryDo(ctx, key, func(ctx context.Context) (any, error) {
		// Prepare dummy data
		sampleData := map[string]any{
			"after": map[string]any{"id": "sample-id"},
		}

		// Use user-provided sample data if available
		if len(userSample) > 0 {
			for k, v := range userSample {
				sampleData[k] = v
			}
		} else {
			// Try to get actual sample data from sink if supported
			if msg, err := r.SampleSinkTable(ctx, cfg, ""); err == nil && msg != nil {
				data := msg.Data()
				if len(data) > 0 {
					for k, v := range data {
						sampleData[k] = v
					}
				}
			}
		}

		// Resolve templates safely
		parameterizedQuery, args := core.ParameterizeTemplate(cfg.Type, query, sampleData)

		// 1. Try if the sink already implements SQLExecutor
		if len(args) == 0 {
			snk, err := r.createSink(ctx, cfg)
			if err == nil {
				defer snk.Close()
				if e, ok := snk.(hermod.SQLExecutor); ok {
					results, err := e.ExecuteSQL(ctx, parameterizedQuery)
					if err == nil {
						return results, nil
					}
					if !errors.Is(err, hermod.ErrNotSupported) {
						return nil, err
					}
				}
			}
		}

		// 2. Fallback to generic SQL execution if it's a supported DB type
		db, err := r.getOrOpenDB(storage.Source{
			ID:     "temp-sink-query-" + cfg.Type + "-" + cfg.ID,
			Type:   cfg.Type,
			Config: cfg.Config,
		})
		if err != nil {
			return nil, err
		}

		rows, err := db.QueryContext(ctx, parameterizedQuery, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		return sqlutil.ScanRows(rows)
	})
	if err != nil {
		return nil, err
	}
	return val.([]map[string]any), nil
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
