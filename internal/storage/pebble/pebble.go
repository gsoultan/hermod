package pebble

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/google/uuid"
	"github.com/user/hermod"
	"github.com/user/hermod/internal/storage"
)

const (
	// defaultCacheBytes bounds Pebble's block cache. This memory lives off the
	// Go heap, so GOMEMLIMIT/GOGC never reclaim it; capping it explicitly keeps
	// Hermod within its lightweight resident-set budget.
	defaultCacheBytes int64 = 32 << 20 // 32 MB
	// defaultMemTableBytes bounds the in-memory write buffer.
	defaultMemTableBytes uint64 = 8 << 20 // 8 MB
	// defaultMaxOpenFiles caps the number of open SSTable file descriptors.
	defaultMaxOpenFiles = 256
)

type pebbleStorage struct {
	db *pebble.DB
}

func NewPebbleStorage(path string) (storage.Storage, error) {
	if path == "" {
		return nil, errors.New("pebble storage path cannot be empty")
	}
	if err := os.MkdirAll(path, 0755); err != nil {
		return nil, fmt.Errorf("failed to create pebble directory %s: %w", path, err)
	}
	cache := pebble.NewCache(cacheBytesFromEnv())
	// Open retains its own reference to the cache; release ours so the cache is
	// freed when the database is closed.
	defer cache.Unref()

	db, err := pebble.Open(path, newPebbleOptions(cache))
	if err != nil {
		return nil, err
	}
	return &pebbleStorage{db: db}, nil
}

// newPebbleOptions returns conservative, explicitly-bounded options so the
// engine's off-heap footprint (block cache, memtables, compaction buffers,
// open files) stays small and predictable instead of using Pebble's larger
// machine-scaled defaults.
func newPebbleOptions(cache *pebble.Cache) *pebble.Options {
	return &pebble.Options{
		Cache:                       cache,
		MemTableSize:                defaultMemTableBytes,
		MemTableStopWritesThreshold: 2,
		MaxOpenFiles:                defaultMaxOpenFiles,
		L0CompactionThreshold:       2,
		MaxConcurrentCompactions:    func() int { return 1 },
	}
}

// cacheBytesFromEnv allows operators to tune the block cache via
// HERMOD_PEBBLE_CACHE_MB, falling back to a small default. Invalid or
// non-positive values are ignored so a misconfiguration cannot disable the
// cache or set an unbounded size.
func cacheBytesFromEnv() int64 {
	if v := os.Getenv("HERMOD_PEBBLE_CACHE_MB"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			return n << 20
		}
	}
	return defaultCacheBytes
}

func (s *pebbleStorage) Init(ctx context.Context) error {
	return nil
}

func (s *pebbleStorage) Ping(ctx context.Context) error {
	return nil
}

func (s *pebbleStorage) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// Log methods

func (s *pebbleStorage) CreateLog(ctx context.Context, l storage.Log) error {
	if l.ID == "" {
		l.ID = uuid.New().String()
	}
	if l.Timestamp.IsZero() {
		l.Timestamp = time.Now()
	}
	data, err := json.Marshal(l)
	if err != nil {
		return err
	}
	// Key: l:<reverse_timestamp>:<uuid>
	key := fmt.Sprintf("l:%020d:%s", 999999999999999999-l.Timestamp.UnixNano(), l.ID)
	return s.db.Set([]byte(key), data, pebble.Sync)
}

func (s *pebbleStorage) CreateLogs(ctx context.Context, logs []storage.Log) error {
	batch := s.db.NewBatch()
	defer batch.Close()

	for _, l := range logs {
		if l.ID == "" {
			l.ID = uuid.New().String()
		}
		if l.Timestamp.IsZero() {
			l.Timestamp = time.Now()
		}
		data, err := json.Marshal(l)
		if err != nil {
			return err
		}
		key := fmt.Sprintf("l:%020d:%s", 999999999999999999-l.Timestamp.UnixNano(), l.ID)
		if err := batch.Set([]byte(key), data, pebble.Sync); err != nil {
			return err
		}
	}
	return batch.Commit(pebble.Sync)
}

func (s *pebbleStorage) ListLogs(ctx context.Context, filter storage.LogFilter) ([]storage.Log, int, error) {
	var logs []storage.Log
	iter, err := s.db.NewIter(&pebble.IterOptions{
		LowerBound: []byte("l:"),
		UpperBound: []byte("m:"),
	})
	if err != nil {
		return nil, 0, err
	}
	defer iter.Close()

	for iter.First(); iter.Valid(); iter.Next() {
		var l storage.Log
		if err := json.Unmarshal(iter.Value(), &l); err != nil {
			continue
		}

		// Apply filters
		if !filter.Since.IsZero() && l.Timestamp.Before(filter.Since) {
			continue
		}
		if !filter.Until.IsZero() && !l.Timestamp.Before(filter.Until) {
			continue
		}
		if filter.SourceID != "" && l.SourceID != filter.SourceID {
			continue
		}
		if filter.SinkID != "" && l.SinkID != filter.SinkID {
			continue
		}
		if filter.WorkflowID != "" && l.WorkflowID != filter.WorkflowID {
			continue
		}
		if filter.WithoutWorkflow && l.WorkflowID != "" {
			continue
		}
		if filter.Level != "" && l.Level != filter.Level {
			continue
		}
		if filter.Action != "" && l.Action != filter.Action {
			continue
		}
		if filter.Search != "" {
			search := strings.ToLower(filter.Search)
			if !strings.Contains(strings.ToLower(l.Message), search) &&
				!strings.Contains(strings.ToLower(l.Action), search) &&
				!strings.Contains(strings.ToLower(l.SourceID), search) &&
				!strings.Contains(strings.ToLower(l.SinkID), search) &&
				!strings.Contains(strings.ToLower(l.WorkflowID), search) {
				continue
			}
		}

		logs = append(logs, l)
	}

	total := len(logs)
	// Pagination
	start := 0
	if filter.Limit > 0 {
		if filter.Page > 1 {
			start = (filter.Page - 1) * filter.Limit
		}
		if start >= len(logs) {
			return []storage.Log{}, total, nil
		}
		end := start + filter.Limit
		if end > len(logs) {
			end = len(logs)
		}
		logs = logs[start:end]
	} else if len(logs) > 100 {
		logs = logs[:100]
	}

	return logs, total, nil
}

func (s *pebbleStorage) DeleteLogs(ctx context.Context, filter storage.LogFilter) error {
	iter, err := s.db.NewIter(&pebble.IterOptions{
		LowerBound: []byte("l:"),
		UpperBound: []byte("m:"),
	})
	if err != nil {
		return err
	}
	defer iter.Close()

	batch := s.db.NewBatch()
	defer batch.Close()
	for iter.First(); iter.Valid(); iter.Next() {
		var l storage.Log
		if err := json.Unmarshal(iter.Value(), &l); err != nil {
			continue
		}

		// Check if matches filter
		matches := true
		if !filter.Since.IsZero() && l.Timestamp.Before(filter.Since) {
			matches = false
		}
		if !filter.Until.IsZero() && !l.Timestamp.Before(filter.Until) {
			matches = false
		}
		if filter.SourceID != "" && l.SourceID != filter.SourceID {
			matches = false
		}
		if filter.SinkID != "" && l.SinkID != filter.SinkID {
			matches = false
		}
		if filter.WorkflowID != "" && l.WorkflowID != filter.WorkflowID {
			matches = false
		}
		if filter.WithoutWorkflow && l.WorkflowID != "" {
			matches = false
		}
		if filter.Level != "" && l.Level != filter.Level {
			matches = false
		}
		if filter.Action != "" && l.Action != filter.Action {
			matches = false
		}

		if matches {
			if err := batch.Delete(iter.Key(), pebble.Sync); err != nil {
				return err
			}
		}
	}
	return batch.Commit(pebble.Sync)
}

func (s *pebbleStorage) PurgeLogs(ctx context.Context, before time.Time) error {
	startKey := fmt.Sprintf("l:%020d", 999999999999999999-before.UnixNano())
	iter, err := s.db.NewIter(&pebble.IterOptions{
		LowerBound: []byte(startKey),
		UpperBound: []byte("m:"),
	})
	if err != nil {
		return err
	}
	defer iter.Close()

	batch := s.db.NewBatch()
	defer batch.Close()
	for iter.First(); iter.Valid(); iter.Next() {
		if err := batch.Delete(iter.Key(), pebble.Sync); err != nil {
			return err
		}
	}
	return batch.Commit(pebble.Sync)
}

// Audit Log methods

func (s *pebbleStorage) CreateAuditLog(ctx context.Context, a storage.AuditLog) error {
	if a.ID == "" {
		a.ID = uuid.New().String()
	}
	if a.Timestamp.IsZero() {
		a.Timestamp = time.Now()
	}
	data, err := json.Marshal(a)
	if err != nil {
		return err
	}
	// Key: a:<reverse_timestamp>:<uuid>
	key := fmt.Sprintf("a:%020d:%s", 999999999999999999-a.Timestamp.UnixNano(), a.ID)
	return s.db.Set([]byte(key), data, pebble.Sync)
}

func (s *pebbleStorage) ListAuditLogs(ctx context.Context, filter storage.AuditFilter) ([]storage.AuditLog, int, error) {
	var logs []storage.AuditLog
	iter, err := s.db.NewIter(&pebble.IterOptions{
		LowerBound: []byte("a:"),
		UpperBound: []byte("b:"),
	})
	if err != nil {
		return nil, 0, err
	}
	defer iter.Close()

	for iter.First(); iter.Valid(); iter.Next() {
		var a storage.AuditLog
		if err := json.Unmarshal(iter.Value(), &a); err != nil {
			continue
		}

		if filter.From != nil && a.Timestamp.Before(*filter.From) {
			continue
		}
		if filter.To != nil && !a.Timestamp.Before(*filter.To) {
			continue
		}
		if filter.UserID != "" && a.UserID != filter.UserID {
			continue
		}
		if filter.Action != "" && a.Action != filter.Action {
			continue
		}
		if filter.EntityType != "" && a.EntityType != filter.EntityType {
			continue
		}
		if filter.EntityID != "" && a.EntityID != filter.EntityID {
			continue
		}

		logs = append(logs, a)
	}

	total := len(logs)
	start := 0
	if filter.Limit > 0 {
		if filter.Page > 1 {
			start = (filter.Page - 1) * filter.Limit
		}
		if start >= len(logs) {
			return []storage.AuditLog{}, total, nil
		}
		end := start + filter.Limit
		if end > len(logs) {
			end = len(logs)
		}
		logs = logs[start:end]
	}

	return logs, total, nil
}

func (s *pebbleStorage) PurgeAuditLogs(ctx context.Context, before time.Time) error {
	startKey := fmt.Sprintf("a:%020d", 999999999999999999-before.UnixNano())
	iter, err := s.db.NewIter(&pebble.IterOptions{
		LowerBound: []byte(startKey),
		UpperBound: []byte("b:"),
	})
	if err != nil {
		return err
	}
	defer iter.Close()

	batch := s.db.NewBatch()
	defer batch.Close()
	for iter.First(); iter.Valid(); iter.Next() {
		if err := batch.Delete(iter.Key(), pebble.Sync); err != nil {
			return err
		}
	}
	return batch.Commit(pebble.Sync)
}

// Trace methods

func (s *pebbleStorage) RecordTraceStep(ctx context.Context, workflowID, messageID string, step hermod.TraceStep) error {
	key := fmt.Sprintf("t:%s:%s", workflowID, messageID)
	val, closer, err := s.db.Get([]byte(key))
	var trace storage.MessageTrace
	if err == nil {
		defer closer.Close()
		_ = json.Unmarshal(val, &trace)
	} else if errors.Is(err, pebble.ErrNotFound) {
		trace = storage.MessageTrace{
			WorkflowID: workflowID,
			MessageID:  messageID,
			CreatedAt:  time.Now(),
		}
	} else {
		return err
	}

	trace.Steps = append(trace.Steps, step)
	data, _ := json.Marshal(trace)
	return s.db.Set([]byte(key), data, pebble.Sync)
}

func (s *pebbleStorage) GetMessageTrace(ctx context.Context, workflowID, messageID string) (storage.MessageTrace, error) {
	key := fmt.Sprintf("t:%s:%s", workflowID, messageID)
	val, closer, err := s.db.Get([]byte(key))
	if err != nil {
		if errors.Is(err, pebble.ErrNotFound) {
			return storage.MessageTrace{}, storage.ErrNotFound
		}
		return storage.MessageTrace{}, err
	}
	defer closer.Close()
	var trace storage.MessageTrace
	if err := json.Unmarshal(val, &trace); err != nil {
		return storage.MessageTrace{}, err
	}
	return trace, nil
}

func (s *pebbleStorage) ListMessageTraces(ctx context.Context, workflowID string, limit, offset int) ([]storage.MessageTrace, error) {
	var traces []storage.MessageTrace
	prefix := []byte(fmt.Sprintf("t:%s:", workflowID))
	iter, err := s.db.NewIter(&pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: append(prefix, 0xFF),
	})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	for iter.First(); iter.Valid(); iter.Next() {
		var trace storage.MessageTrace
		if err := json.Unmarshal(iter.Value(), &trace); err != nil {
			continue
		}
		traces = append(traces, trace)
	}
	// Sort by CreatedAt desc before applying paging so the order is stable.
	sort.Slice(traces, func(i, j int) bool {
		return traces[i].CreatedAt.After(traces[j].CreatedAt)
	})

	if offset < 0 {
		offset = 0
	}
	if offset >= len(traces) {
		return []storage.MessageTrace{}, nil
	}
	traces = traces[offset:]
	if limit > 0 && len(traces) > limit {
		traces = traces[:limit]
	}
	return traces, nil
}

func (s *pebbleStorage) PurgeMessageTraces(ctx context.Context, before time.Time) error {
	iter, err := s.db.NewIter(&pebble.IterOptions{
		LowerBound: []byte("t:"),
		UpperBound: []byte("u:"),
	})
	if err != nil {
		return err
	}
	defer iter.Close()

	batch := s.db.NewBatch()
	for iter.First(); iter.Valid(); iter.Next() {
		var trace storage.MessageTrace
		if err := json.Unmarshal(iter.Value(), &trace); err != nil {
			continue
		}
		if trace.CreatedAt.Before(before) {
			if err := batch.Delete(iter.Key(), pebble.Sync); err != nil {
				return err
			}
		}
	}
	return batch.Commit(pebble.Sync)
}

// Stubs for non-log methods

func (s *pebbleStorage) ListSources(ctx context.Context, filter storage.CommonFilter) ([]storage.Source, int, error) {
	return nil, 0, errors.New("not implemented")
}
func (s *pebbleStorage) CreateSource(ctx context.Context, src storage.Source) error {
	return errors.New("not implemented")
}
func (s *pebbleStorage) UpdateSource(ctx context.Context, src storage.Source) error {
	return errors.New("not implemented")
}
func (s *pebbleStorage) UpdateSourceStatus(ctx context.Context, id string, status string) error {
	return errors.New("not implemented")
}
func (s *pebbleStorage) UpdateSourceState(ctx context.Context, id string, state map[string]string) error {
	return errors.New("not implemented")
}
func (s *pebbleStorage) DeleteSource(ctx context.Context, id string) error {
	return errors.New("not implemented")
}
func (s *pebbleStorage) GetSource(ctx context.Context, id string) (storage.Source, error) {
	return storage.Source{}, errors.New("not implemented")
}
func (s *pebbleStorage) ListSinks(ctx context.Context, filter storage.CommonFilter) ([]storage.Sink, int, error) {
	return nil, 0, errors.New("not implemented")
}
func (s *pebbleStorage) CreateSink(ctx context.Context, snk storage.Sink) error {
	return errors.New("not implemented")
}
func (s *pebbleStorage) UpdateSink(ctx context.Context, snk storage.Sink) error {
	return errors.New("not implemented")
}
func (s *pebbleStorage) UpdateSinkStatus(ctx context.Context, id string, status string) error {
	return errors.New("not implemented")
}
func (s *pebbleStorage) DeleteSink(ctx context.Context, id string) error {
	return errors.New("not implemented")
}
func (s *pebbleStorage) GetSink(ctx context.Context, id string) (storage.Sink, error) {
	return storage.Sink{}, errors.New("not implemented")
}
func (s *pebbleStorage) ListUsers(ctx context.Context, filter storage.CommonFilter) ([]storage.User, int, error) {
	return nil, 0, errors.New("not implemented")
}
func (s *pebbleStorage) CreateUser(ctx context.Context, user storage.User) error {
	return errors.New("not implemented")
}
func (s *pebbleStorage) UpdateUser(ctx context.Context, user storage.User) error {
	return errors.New("not implemented")
}
func (s *pebbleStorage) DeleteUser(ctx context.Context, id string) error {
	return errors.New("not implemented")
}
func (s *pebbleStorage) GetUser(ctx context.Context, id string) (storage.User, error) {
	return storage.User{}, errors.New("not implemented")
}
func (s *pebbleStorage) GetUserByUsername(ctx context.Context, username string) (storage.User, error) {
	return storage.User{}, errors.New("not implemented")
}
func (s *pebbleStorage) GetUserByEmail(ctx context.Context, email string) (storage.User, error) {
	return storage.User{}, errors.New("not implemented")
}
func (s *pebbleStorage) ListVHosts(ctx context.Context, filter storage.CommonFilter) ([]storage.VHost, int, error) {
	return nil, 0, errors.New("not implemented")
}
func (s *pebbleStorage) CreateVHost(ctx context.Context, vhost storage.VHost) error {
	return errors.New("not implemented")
}
func (s *pebbleStorage) UpdateVHost(ctx context.Context, vhost storage.VHost) error {
	return errors.New("not implemented")
}
func (s *pebbleStorage) DeleteVHost(ctx context.Context, id string) error {
	return errors.New("not implemented")
}
func (s *pebbleStorage) GetVHost(ctx context.Context, id string) (storage.VHost, error) {
	return storage.VHost{}, errors.New("not implemented")
}
func (s *pebbleStorage) ListWorkflows(ctx context.Context, filter storage.CommonFilter) ([]storage.Workflow, int, error) {
	return nil, 0, errors.New("not implemented")
}
func (s *pebbleStorage) ListWorkspaces(ctx context.Context) ([]storage.Workspace, error) {
	return nil, errors.New("not implemented")
}
func (s *pebbleStorage) CreateWorkspace(ctx context.Context, ws storage.Workspace) error {
	return errors.New("not implemented")
}
func (s *pebbleStorage) GetWorkspace(ctx context.Context, id string) (storage.Workspace, error) {
	return storage.Workspace{}, errors.New("not implemented")
}
func (s *pebbleStorage) DeleteWorkspace(ctx context.Context, id string) error {
	return errors.New("not implemented")
}
func (s *pebbleStorage) CreateWorkflow(ctx context.Context, wf storage.Workflow) error {
	return errors.New("not implemented")
}
func (s *pebbleStorage) UpdateWorkflow(ctx context.Context, wf storage.Workflow) error {
	return errors.New("not implemented")
}
func (s *pebbleStorage) UpdateWorkflowStatus(ctx context.Context, id string, status string) error {
	return errors.New("not implemented")
}
func (s *pebbleStorage) UpdateWorkflowStats(ctx context.Context, id string, processed, numErrors, lag uint64) error {
	return errors.New("not implemented")
}
func (s *pebbleStorage) DeleteWorkflow(ctx context.Context, id string) error {
	return errors.New("not implemented")
}
func (s *pebbleStorage) GetWorkflow(ctx context.Context, id string) (storage.Workflow, error) {
	return storage.Workflow{}, errors.New("not implemented")
}
func (s *pebbleStorage) AcquireWorkflowLease(ctx context.Context, workflowID, ownerID string, ttlSeconds int) (bool, error) {
	return false, errors.New("not implemented")
}
func (s *pebbleStorage) RenewWorkflowLease(ctx context.Context, workflowID, ownerID string, ttlSeconds int) (bool, error) {
	return false, errors.New("not implemented")
}
func (s *pebbleStorage) ReleaseWorkflowLease(ctx context.Context, workflowID, ownerID string) error {
	return errors.New("not implemented")
}
func (s *pebbleStorage) ListWorkers(ctx context.Context, filter storage.CommonFilter) ([]storage.Worker, int, error) {
	return nil, 0, errors.New("not implemented")
}
func (s *pebbleStorage) CreateWorker(ctx context.Context, worker storage.Worker) error {
	return errors.New("not implemented")
}
func (s *pebbleStorage) UpdateWorker(ctx context.Context, worker storage.Worker) error {
	return errors.New("not implemented")
}
func (s *pebbleStorage) UpdateWorkerHeartbeat(ctx context.Context, id string, cpu, mem float64) error {
	return errors.New("not implemented")
}
func (s *pebbleStorage) DeleteWorker(ctx context.Context, id string) error {
	return errors.New("not implemented")
}
func (s *pebbleStorage) GetWorker(ctx context.Context, id string) (storage.Worker, error) {
	return storage.Worker{}, errors.New("not implemented")
}
func (s *pebbleStorage) ListWebhookRequests(ctx context.Context, filter storage.WebhookRequestFilter) ([]storage.WebhookRequest, int, error) {
	return nil, 0, errors.New("not implemented")
}
func (s *pebbleStorage) CreateWebhookRequest(ctx context.Context, req storage.WebhookRequest) error {
	return errors.New("not implemented")
}
func (s *pebbleStorage) GetWebhookRequest(ctx context.Context, id string) (storage.WebhookRequest, error) {
	return storage.WebhookRequest{}, errors.New("not implemented")
}
func (s *pebbleStorage) DeleteWebhookRequests(ctx context.Context, filter storage.WebhookRequestFilter) error {
	return errors.New("not implemented")
}
func (s *pebbleStorage) CreateFormSubmission(ctx context.Context, sub storage.FormSubmission) error {
	return errors.New("not implemented")
}
func (s *pebbleStorage) ListFormSubmissions(ctx context.Context, filter storage.FormSubmissionFilter) ([]storage.FormSubmission, int, error) {
	return nil, 0, errors.New("not implemented")
}
func (s *pebbleStorage) GetFormSubmission(ctx context.Context, id string) (storage.FormSubmission, error) {
	return storage.FormSubmission{}, errors.New("not implemented")
}
func (s *pebbleStorage) UpdateFormSubmissionStatus(ctx context.Context, id string, status string) error {
	return errors.New("not implemented")
}
func (s *pebbleStorage) DeleteFormSubmissions(ctx context.Context, filter storage.FormSubmissionFilter) error {
	return errors.New("not implemented")
}
func (s *pebbleStorage) GetSetting(ctx context.Context, key string) (string, error) {
	return "", errors.New("not implemented")
}
func (s *pebbleStorage) SaveSetting(ctx context.Context, key string, value string) error {
	return errors.New("not implemented")
}
func (s *pebbleStorage) UpdateNodeState(ctx context.Context, workflowID, nodeID string, state any) error {
	return errors.New("not implemented")
}
func (s *pebbleStorage) GetNodeStates(ctx context.Context, workflowID string) (map[string]any, error) {
	return nil, errors.New("not implemented")
}
func (s *pebbleStorage) ListSchemas(ctx context.Context, name string) ([]storage.Schema, error) {
	return nil, errors.New("not implemented")
}
func (s *pebbleStorage) ListAllSchemas(ctx context.Context) ([]storage.Schema, error) {
	return nil, errors.New("not implemented")
}
func (s *pebbleStorage) GetSchema(ctx context.Context, name string, version int) (storage.Schema, error) {
	return storage.Schema{}, errors.New("not implemented")
}
func (s *pebbleStorage) GetLatestSchema(ctx context.Context, name string) (storage.Schema, error) {
	return storage.Schema{}, errors.New("not implemented")
}
func (s *pebbleStorage) CreateSchema(ctx context.Context, schema storage.Schema) error {
	return errors.New("not implemented")
}
func (s *pebbleStorage) CreateWorkflowVersion(ctx context.Context, version storage.WorkflowVersion) error {
	return errors.New("not implemented")
}
func (s *pebbleStorage) ListWorkflowVersions(ctx context.Context, workflowID string) ([]storage.WorkflowVersion, error) {
	return nil, errors.New("not implemented")
}
func (s *pebbleStorage) GetWorkflowVersion(ctx context.Context, workflowID string, version int) (storage.WorkflowVersion, error) {
	return storage.WorkflowVersion{}, errors.New("not implemented")
}
func (s *pebbleStorage) CreateOutboxItem(ctx context.Context, item storage.OutboxItem) error {
	return errors.New("not implemented")
}
func (s *pebbleStorage) ListOutboxItems(ctx context.Context, status string, limit int) ([]storage.OutboxItem, error) {
	return nil, errors.New("not implemented")
}
func (s *pebbleStorage) DeleteOutboxItem(ctx context.Context, id string) error {
	return errors.New("not implemented")
}
func (s *pebbleStorage) UpdateOutboxItem(ctx context.Context, item storage.OutboxItem) error {
	return errors.New("not implemented")
}
func (s *pebbleStorage) GetLineage(ctx context.Context) ([]storage.LineageEdge, error) {
	return nil, errors.New("not implemented")
}
func (s *pebbleStorage) ListPlugins(ctx context.Context) ([]storage.Plugin, error) {
	return nil, errors.New("not implemented")
}
func (s *pebbleStorage) GetPlugin(ctx context.Context, id string) (storage.Plugin, error) {
	return storage.Plugin{}, errors.New("not implemented")
}
func (s *pebbleStorage) InstallPlugin(ctx context.Context, id string) error {
	return errors.New("not implemented")
}
func (s *pebbleStorage) UninstallPlugin(ctx context.Context, id string) error {
	return errors.New("not implemented")
}

func (s *pebbleStorage) GetDashboardStats(ctx context.Context, vhost string) (storage.DashboardStats, error) {
	return storage.DashboardStats{}, errors.New("not implemented")
}
func (s *pebbleStorage) ListApprovals(ctx context.Context, filter storage.ApprovalFilter) ([]storage.Approval, int, error) {
	return nil, 0, errors.New("not implemented")
}
func (s *pebbleStorage) CreateApproval(ctx context.Context, app storage.Approval) error {
	return errors.New("not implemented")
}
func (s *pebbleStorage) GetApproval(ctx context.Context, id string) (storage.Approval, error) {
	return storage.Approval{}, errors.New("not implemented")
}
func (s *pebbleStorage) UpdateApprovalStatus(ctx context.Context, id string, status string, processedBy string, notes string, formData map[string]any) error {
	return errors.New("not implemented")
}

func (s *pebbleStorage) CreateSuspendedMessage(ctx context.Context, m storage.SuspendedMessage) error {
	return errors.New("not implemented")
}

func (s *pebbleStorage) ListSuspendedMessages(ctx context.Context, workflowID string, before time.Time) ([]storage.SuspendedMessage, error) {
	return nil, nil
}

func (s *pebbleStorage) DeleteSuspendedMessage(ctx context.Context, id string) error {
	return errors.New("not implemented")
}
func (s *pebbleStorage) DeleteApproval(ctx context.Context, id string) error {
	return errors.New("not implemented")
}
