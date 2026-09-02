package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"runtime/debug"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/factory"
	"github.com/gsoultan/hermod/pkg/comm/transformer/core"
	"github.com/gsoultan/hermod/pkg/infra/sqlutil"
	"golang.org/x/sync/singleflight"
)

var ErrOperationPanicked = errors.New("operation panicked")

type ComponentCreator interface {
	CreateSource(ctx context.Context, cfg factory.SourceConfig) (hermod.Source, error)
	CreateSink(ctx context.Context, cfg factory.SinkConfig) (hermod.Sink, error)
	GetSourceFactoryConfig(ctx context.Context, id string) (factory.SourceConfig, error)
	GetDB(ctx context.Context, typeName string, config map[string]string) (*sql.DB, error)
}

type DiscoveryService struct {
	creator ComponentCreator
	sf      singleflight.Group
}

func NewDiscoveryService(creator ComponentCreator) *DiscoveryService {
	return &DiscoveryService{
		creator: creator,
	}
}

// openSource builds a source and refuses one that was never built.
//
// A factory returning (nil, nil) is a bug in that factory, but the caller here
// immediately calls a method on the result, so the bug surfaced as a nil
// dereference inside discovery rather than as a message naming the connector.
// TestSource checked for this; nothing else did.
func (s *DiscoveryService) openSource(ctx context.Context, cfg factory.SourceConfig) (hermod.Source, error) {
	src, err := s.creator.CreateSource(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if src == nil {
		return nil, fmt.Errorf("source type %q produced a nil source", cfg.Type)
	}
	return src, nil
}

// openSink is openSource for the other side of the pipeline.
func (s *DiscoveryService) openSink(ctx context.Context, cfg factory.SinkConfig) (hermod.Sink, error) {
	snk, err := s.creator.CreateSink(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if snk == nil {
		return nil, fmt.Errorf("sink type %q produced a nil sink", cfg.Type)
	}
	return snk, nil
}

// discoveryValue converts a discovery result to the type its caller expects.
//
// Every method here ended with an unchecked type assertion on an `any`. Most
// were safe by construction, but a connector that reports "nothing found" as
// (nil, nil) — which the Sampler contract allows — produced a nil `any` with no
// error, and the assertion panicked. That panic is raised in the caller's
// goroutine, outside the recover() that guards the work itself, so it reached
// the HTTP handler. A connector returning something unexpected should be a
// message naming the connector, not a crash in the code that called it.
func discoveryValue[T any](val any, what string) (T, error) {
	var zero T
	if val == nil {
		return zero, fmt.Errorf("%s returned no result and gave no reason", what)
	}
	typed, ok := val.(T)
	if !ok {
		return zero, fmt.Errorf("%s returned an unexpected %T", what, val)
	}
	return typed, nil
}

func (s *DiscoveryService) discoveryKey(prefix string, cfg any) string {
	b, _ := json.Marshal(cfg)
	return prefix + ":" + string(b)
}

func (s *DiscoveryService) discoveryDo(ctx context.Context, key string, fn func(ctx context.Context) (any, error)) (any, error) {
	ch := s.sf.DoChan(key, func() (any, error) {
		// Use a background timeout context for the actual work to prevent
		// goroutine leaks when the first caller cancels.
		workCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return RunWithContext(workCtx, func() (any, error) {
			return fn(workCtx)
		})
	})

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-ch:
		return res.Val, res.Err
	}
}

func (s *DiscoveryService) TestSource(ctx context.Context, cfg factory.SourceConfig) error {
	key := s.discoveryKey("test-source", cfg)
	_, err := s.discoveryDo(ctx, key, func(ctx context.Context) (any, error) {
		src, err := s.openSource(ctx, cfg)
		if err != nil {
			return struct{}{}, err
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

func (s *DiscoveryService) TestSink(ctx context.Context, cfg factory.SinkConfig) error {
	if cfg.Type == "stdout" {
		return nil
	}

	key := s.discoveryKey("test-sink", cfg)
	_, err := s.discoveryDo(ctx, key, func(ctx context.Context) (any, error) {
		snk, err := s.openSink(ctx, cfg)
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

func RunWithContext[T any](ctx context.Context, fn func() (T, error)) (T, error) {
	type result struct {
		val T
		err error
	}
	ch := make(chan result, 1)

	// Use Go 1.26 WaitGroup.Go for standardized goroutine management.
	var wg sync.WaitGroup
	wg.Go(func() {
		defer func() {
			if rec := recover(); rec != nil {
				var zero T
				select {
				case ch <- result{
					val: zero,
					err: fmt.Errorf("%w: %v\n%s", ErrOperationPanicked, rec, debug.Stack()),
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
	})

	select {
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	case res := <-ch:
		return res.val, res.err
	}
}

func (s *DiscoveryService) DiscoverDatabases(ctx context.Context, cfg factory.SourceConfig) ([]string, error) {
	key := s.discoveryKey("discover-dbs", cfg)
	val, err := s.discoveryDo(ctx, key, func(ctx context.Context) (any, error) {
		src, err := s.openSource(ctx, cfg)
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
	return discoveryValue[[]string](val, "listing the databases on source type "+cfg.Type+"")
}

func (s *DiscoveryService) DiscoverTables(ctx context.Context, cfg factory.SourceConfig) ([]string, error) {
	key := s.discoveryKey("discover-tables", cfg)
	val, err := s.discoveryDo(ctx, key, func(ctx context.Context) (any, error) {
		src, err := s.openSource(ctx, cfg)
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
	return discoveryValue[[]string](val, "listing the tables on source type "+cfg.Type+"")
}

func (s *DiscoveryService) DiscoverSourceColumns(ctx context.Context, cfg factory.SourceConfig, table string) ([]hermod.ColumnInfo, error) {
	key := s.discoveryKey("discover-columns", struct {
		Cfg   factory.SourceConfig
		Table string
	}{cfg, table})
	val, err := s.discoveryDo(ctx, key, func(ctx context.Context) (any, error) {
		src, err := s.openSource(ctx, cfg)
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
	return discoveryValue[[]hermod.ColumnInfo](val, "listing the columns of "+table+" on source type "+cfg.Type+"")
}

func (s *DiscoveryService) DiscoverReplicationSlots(ctx context.Context, cfg factory.SourceConfig) ([]hermod.ReplicationSlotInfo, error) {
	key := s.discoveryKey("discover-slots", cfg)
	val, err := s.discoveryDo(ctx, key, func(ctx context.Context) (any, error) {
		src, err := s.openSource(ctx, cfg)
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
	return discoveryValue[[]hermod.ReplicationSlotInfo](val, "listing the replication slots on source type "+cfg.Type+"")
}

func (s *DiscoveryService) DiscoverPublications(ctx context.Context, cfg factory.SourceConfig) ([]hermod.PublicationInfo, error) {
	key := s.discoveryKey("discover-pubs", cfg)
	val, err := s.discoveryDo(ctx, key, func(ctx context.Context) (any, error) {
		src, err := s.openSource(ctx, cfg)
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
	return discoveryValue[[]hermod.PublicationInfo](val, "listing the publications on source type "+cfg.Type+"")
}

func (s *DiscoveryService) DiscoverSinkDatabases(ctx context.Context, cfg factory.SinkConfig) ([]string, error) {
	key := s.discoveryKey("discover-sink-dbs", cfg)
	val, err := s.discoveryDo(ctx, key, func(ctx context.Context) (any, error) {
		snk, err := s.openSink(ctx, cfg)
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
	return discoveryValue[[]string](val, "listing the databases on sink type "+cfg.Type+"")
}

func (s *DiscoveryService) DiscoverSinkTables(ctx context.Context, cfg factory.SinkConfig) ([]string, error) {
	key := s.discoveryKey("discover-sink-tables", cfg)
	val, err := s.discoveryDo(ctx, key, func(ctx context.Context) (any, error) {
		snk, err := s.openSink(ctx, cfg)
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
	return discoveryValue[[]string](val, "listing the tables on sink type "+cfg.Type+"")
}

func (s *DiscoveryService) DiscoverSinkColumns(ctx context.Context, cfg factory.SinkConfig, table string) ([]hermod.ColumnInfo, error) {
	key := s.discoveryKey("discover-sink-columns", struct {
		Cfg   factory.SinkConfig
		Table string
	}{cfg, table})
	val, err := s.discoveryDo(ctx, key, func(ctx context.Context) (any, error) {
		snk, err := s.openSink(ctx, cfg)
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
	return discoveryValue[[]hermod.ColumnInfo](val, "listing the columns of "+table+" on sink type "+cfg.Type+"")
}

func (s *DiscoveryService) SampleTable(ctx context.Context, cfg factory.SourceConfig, table string) (hermod.Message, error) {
	key := s.discoveryKey("sample-source", struct {
		Cfg   factory.SourceConfig
		Table string
	}{cfg, table})
	val, err := s.discoveryDo(ctx, key, func(ctx context.Context) (any, error) {
		return s.sampleTable(ctx, cfg, table)
	})
	if err != nil {
		return nil, err
	}
	return discoveryValue[hermod.Message](val, "sampling "+table+" on source type "+cfg.Type+"")
}

func (s *DiscoveryService) sampleTable(ctx context.Context, cfg factory.SourceConfig, table string) (hermod.Message, error) {
	src, err := s.openSource(ctx, cfg)
	if err != nil {
		return nil, err
	}
	defer src.Close()

	if sampler, ok := src.(hermod.Sampler); ok {
		msg, err := sampler.Sample(ctx, table)
		if err != nil {
			return nil, err
		}
		// The Sampler contract allows reporting an empty table as (nil, nil).
		// Passed on as-is it became a nil result with no error, which the caller
		// asserted to hermod.Message and panicked on. Say what the Browser path
		// already says.
		if msg == nil {
			return nil, fmt.Errorf("no data found in table %s", table)
		}
		return msg, nil
	}
	if browser, ok := src.(hermod.Browser); ok {
		msgs, err := browser.Browse(ctx, table, 1)
		if err != nil {
			return nil, err
		}
		if len(msgs) > 0 {
			return msgs[0], nil
		}
		return nil, fmt.Errorf("no data found in table %s", table)
	}
	return nil, fmt.Errorf("source type %s does not support sampling", cfg.Type)
}

func (s *DiscoveryService) SampleSinkTable(ctx context.Context, cfg factory.SinkConfig, table string) (hermod.Message, error) {
	msgs, err := s.BrowseSinkTable(ctx, cfg, table, 1)
	if err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, fmt.Errorf("no data found in sink table %s", table)
	}
	return msgs[0], nil
}

func (s *DiscoveryService) BrowseSinkTable(ctx context.Context, cfg factory.SinkConfig, table string, limit int) ([]hermod.Message, error) {
	key := s.discoveryKey("browse-sink", struct {
		Cfg   factory.SinkConfig
		Table string
		Limit int
	}{cfg, table, limit})
	val, err := s.discoveryDo(ctx, key, func(ctx context.Context) (any, error) {
		snk, err := s.openSink(ctx, cfg)
		if err != nil {
			return nil, err
		}
		defer snk.Close()

		// A browse that was attempted and failed is reported as such. The error
		// used to be discarded and the fall-through said the sink "does not
		// support browsing" — which is false, and sends the operator looking for
		// a missing feature when the database told them their table name or their
		// grants were wrong.
		var browseErr error
		if b, ok := snk.(hermod.Browser); ok {
			msgs, err := b.Browse(ctx, table, limit)
			if err == nil {
				return msgs, nil
			}
			browseErr = err
		}

		// Sampling is still worth trying for a single row, since some sinks
		// implement one and not the other.
		if sam, ok := snk.(hermod.Sampler); ok && limit == 1 {
			msg, err := sam.Sample(ctx, table)
			if err != nil {
				return nil, err
			}
			if msg == nil {
				return nil, fmt.Errorf("no data found in sink table %s", table)
			}
			return []hermod.Message{msg}, nil
		}

		if browseErr != nil {
			return nil, browseErr
		}
		return nil, fmt.Errorf("sink type %s does not support browsing", cfg.Type)
	})
	if err != nil {
		return nil, err
	}
	return discoveryValue[[]hermod.Message](val, "browsing "+table+" on sink type "+cfg.Type+"")
}

func (s *DiscoveryService) ExecuteSQL(ctx context.Context, cfg factory.SourceConfig, query string, userSample map[string]any) ([]map[string]any, error) {
	key := s.discoveryKey("exec-sql", struct {
		Cfg    factory.SourceConfig
		Query  string
		Sample map[string]any
	}{cfg, query, userSample})
	val, err := s.discoveryDo(ctx, key, func(ctx context.Context) (any, error) {
		// Start with default fallback
		sampleData := map[string]any{
			"after": map[string]any{"id": uuid.NewString()},
		}

		// Use user-provided sample data if available
		if len(userSample) > 0 {
			maps.Copy(sampleData, userSample)
		} else {
			// Fallback to table sampling
			if msg, err := s.sampleTable(ctx, cfg, ""); err == nil && msg != nil {
				data := msg.Data()
				if len(data) > 0 {
					maps.Copy(sampleData, data)
				}
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

		driver := cfg.Type
		if driver == "batch_sql" {
			if underlyingID := cfg.Config["source_id"]; underlyingID != "" {
				if underlying, err := s.creator.GetSourceFactoryConfig(ctx, underlyingID); err == nil {
					driver = underlying.Type
				}
			}
		}

		parameterizedQuery, args := core.ParameterizeTemplate(driver, query, sampleData)

		if len(args) == 0 {
			src, err := s.openSource(ctx, cfg)
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

		// Fallback to generic SQL execution
		db, err := s.creator.GetDB(ctx, cfg.Type, cfg.Config)
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
	return discoveryValue[[]map[string]any](val, "running that query on source type "+cfg.Type+"")
}

func (s *DiscoveryService) ExecuteSinkSQL(ctx context.Context, cfg factory.SinkConfig, query string, userSample map[string]any) ([]map[string]any, error) {
	key := s.discoveryKey("exec-sink-sql", struct {
		Cfg    factory.SinkConfig
		Query  string
		Sample map[string]any
	}{cfg, query, userSample})
	val, err := s.discoveryDo(ctx, key, func(ctx context.Context) (any, error) {
		sampleData := map[string]any{
			"after": map[string]any{"id": uuid.NewString()},
		}

		if len(userSample) > 0 {
			maps.Copy(sampleData, userSample)
		} else {
			if msg, err := s.SampleSinkTable(ctx, cfg, ""); err == nil && msg != nil {
				data := msg.Data()
				if len(data) > 0 {
					maps.Copy(sampleData, data)
				}
			}
		}

		parameterizedQuery, args := core.ParameterizeTemplate(cfg.Type, query, sampleData)

		if len(args) == 0 {
			snk, err := s.openSink(ctx, cfg)
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

		db, err := s.creator.GetDB(ctx, cfg.Type, cfg.Config)
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
	return discoveryValue[[]map[string]any](val, "running that query on sink type "+cfg.Type+"")
}

func (s *DiscoveryService) ExecSinkStatement(ctx context.Context, cfg factory.SinkConfig, stmt string) error {
	db, err := s.creator.GetDB(ctx, cfg.Type, cfg.Config)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, stmt)
	return err
}
