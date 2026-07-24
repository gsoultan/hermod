package registry

import (
	"context"
	"encoding/json"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/discovery/service"
	"github.com/user/hermod/internal/factory"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/comm/message"
)

// errOperationPanicked is returned when a source/sink connectivity or discovery
// operation panics. It allows callers to recognise (via errors.Is) that the
// failure originated from a recovered panic rather than a regular error.
var errOperationPanicked = service.ErrOperationPanicked

// --- Test Connectivity ---

func (r *Registry) discoveryKey(prefix string, cfg any) string {
	b, _ := json.Marshal(cfg)
	return prefix + ":" + string(b)
}

func (r *Registry) TestSource(ctx context.Context, cfg factory.SourceConfig) error {
	return r.discoveryService.TestSource(ctx, cfg)
}

func (r *Registry) TestSink(ctx context.Context, cfg factory.SinkConfig) error {
	return r.discoveryService.TestSink(ctx, cfg)
}

// --- Source Discovery ---

func (r *Registry) DiscoverDatabases(ctx context.Context, cfg factory.SourceConfig) ([]string, error) {
	return r.discoveryService.DiscoverDatabases(ctx, cfg)
}

func (r *Registry) DiscoverTables(ctx context.Context, cfg factory.SourceConfig) ([]string, error) {
	return r.discoveryService.DiscoverTables(ctx, cfg)
}

func (r *Registry) DiscoverSourceColumns(ctx context.Context, cfg factory.SourceConfig, table string) ([]hermod.ColumnInfo, error) {
	return r.discoveryService.DiscoverSourceColumns(ctx, cfg, table)
}

func (r *Registry) DiscoverReplicationSlots(ctx context.Context, cfg factory.SourceConfig) ([]hermod.ReplicationSlotInfo, error) {
	return r.discoveryService.DiscoverReplicationSlots(ctx, cfg)
}

func (r *Registry) DiscoverPublications(ctx context.Context, cfg factory.SourceConfig) ([]hermod.PublicationInfo, error) {
	return r.discoveryService.DiscoverPublications(ctx, cfg)
}

// --- Sink Discovery ---

func (r *Registry) DiscoverSinkDatabases(ctx context.Context, cfg factory.SinkConfig) ([]string, error) {
	return r.discoveryService.DiscoverSinkDatabases(ctx, cfg)
}

func (r *Registry) DiscoverSinkTables(ctx context.Context, cfg factory.SinkConfig) ([]string, error) {
	return r.discoveryService.DiscoverSinkTables(ctx, cfg)
}

func (r *Registry) DiscoverSinkColumns(ctx context.Context, cfg factory.SinkConfig, table string) ([]hermod.ColumnInfo, error) {
	return r.discoveryService.DiscoverSinkColumns(ctx, cfg, table)
}

// --- Sampling & Browsing ---

func (r *Registry) SampleTable(ctx context.Context, cfg factory.SourceConfig, table string) (hermod.Message, error) {
	return r.discoveryService.SampleTable(ctx, cfg, table)
}

func (r *Registry) SampleSinkTable(ctx context.Context, cfg factory.SinkConfig, table string) (hermod.Message, error) {
	return r.discoveryService.SampleSinkTable(ctx, cfg, table)
}

func (r *Registry) BrowseSinkTable(ctx context.Context, cfg factory.SinkConfig, table string, limit int) ([]hermod.Message, error) {
	return r.discoveryService.BrowseSinkTable(ctx, cfg, table, limit)
}

// --- SQL Execution ---

func (r *Registry) ExecuteSQL(ctx context.Context, cfg factory.SourceConfig, query string, userSample map[string]any) ([]map[string]any, error) {
	return r.discoveryService.ExecuteSQL(ctx, cfg, query, userSample)
}

func (r *Registry) ExecuteSinkSQL(ctx context.Context, cfg factory.SinkConfig, query string, userSample map[string]any) ([]map[string]any, error) {
	return r.discoveryService.ExecuteSinkSQL(ctx, cfg, query, userSample)
}

func (r *Registry) ExecSinkStatement(ctx context.Context, cfg factory.SinkConfig, stmt string) error {
	return r.discoveryService.ExecSinkStatement(ctx, cfg, stmt)
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
