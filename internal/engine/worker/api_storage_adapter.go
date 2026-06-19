package worker

import (
	"context"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/storage"
)

// apiStorage adapts a platform *WorkerAPIClient to the full storage.Storage
// interface so a remote worker's registry can resolve sources, sinks and
// workflows from the platform API instead of a local database.
//
// The embedded *WorkerAPIClient already provides the network-backed
// operations a worker needs (GetSource, GetSink, GetWorkflow, ListSources,
// ListSinks, ListWorkflows, lease management, logging, etc.). The remaining
// storage.Storage methods are intentionally implemented as safe no-ops or
// "not found" responses: a remote worker is not the system of record for those
// entities, and the registry only invokes the supported subset while starting
// and running workflows.
type apiStorage struct {
	*WorkerAPIClient
}

// NewAPIStorage wraps a platform API client so it satisfies storage.Storage.
// It lets a remote worker's registry resolve sources, sinks and workflows from
// the platform API when no local database is available.
func NewAPIStorage(client *WorkerAPIClient) storage.Storage {
	return &apiStorage{WorkerAPIClient: client}
}

// Init is a no-op: the platform owns schema initialization.
func (a *apiStorage) Init(ctx context.Context) error { return nil }

// Ping is a no-op for the API-backed adapter.
func (a *apiStorage) Ping(ctx context.Context) error { return nil }

// --- Sources (unsupported mutations) ---

func (a *apiStorage) CreateSource(ctx context.Context, src storage.Source) error { return nil }
func (a *apiStorage) UpdateSourceStatus(ctx context.Context, id, status string) error {
	return nil
}
func (a *apiStorage) UpdateSourceState(ctx context.Context, id string, state map[string]string) error {
	return nil
}
func (a *apiStorage) DeleteSource(ctx context.Context, id string) error { return nil }

// --- Sinks (unsupported mutations) ---

func (a *apiStorage) CreateSink(ctx context.Context, snk storage.Sink) error        { return nil }
func (a *apiStorage) UpdateSinkStatus(ctx context.Context, id, status string) error { return nil }
func (a *apiStorage) DeleteSink(ctx context.Context, id string) error               { return nil }

// --- Users ---

func (a *apiStorage) ListUsers(ctx context.Context, filter storage.CommonFilter) ([]storage.User, int, error) {
	return nil, 0, nil
}
func (a *apiStorage) CreateUser(ctx context.Context, user storage.User) error { return nil }
func (a *apiStorage) UpdateUser(ctx context.Context, user storage.User) error { return nil }
func (a *apiStorage) DeleteUser(ctx context.Context, id string) error         { return nil }
func (a *apiStorage) GetUser(ctx context.Context, id string) (storage.User, error) {
	return storage.User{}, storage.ErrNotFound
}
func (a *apiStorage) GetUserByUsername(ctx context.Context, username string) (storage.User, error) {
	return storage.User{}, storage.ErrNotFound
}
func (a *apiStorage) GetUserByEmail(ctx context.Context, email string) (storage.User, error) {
	return storage.User{}, storage.ErrNotFound
}

// --- VHosts ---

func (a *apiStorage) ListVHosts(ctx context.Context, filter storage.CommonFilter) ([]storage.VHost, int, error) {
	return nil, 0, nil
}
func (a *apiStorage) CreateVHost(ctx context.Context, vhost storage.VHost) error { return nil }
func (a *apiStorage) UpdateVHost(ctx context.Context, vhost storage.VHost) error { return nil }
func (a *apiStorage) DeleteVHost(ctx context.Context, id string) error           { return nil }
func (a *apiStorage) GetVHost(ctx context.Context, id string) (storage.VHost, error) {
	return storage.VHost{}, storage.ErrNotFound
}

// --- Workspaces & Workflows (unsupported mutations) ---

func (a *apiStorage) ListWorkspaces(ctx context.Context) ([]storage.Workspace, error) {
	return nil, nil
}
func (a *apiStorage) CreateWorkspace(ctx context.Context, ws storage.Workspace) error { return nil }
func (a *apiStorage) GetWorkspace(ctx context.Context, id string) (storage.Workspace, error) {
	return storage.Workspace{}, storage.ErrNotFound
}
func (a *apiStorage) DeleteWorkspace(ctx context.Context, id string) error          { return nil }
func (a *apiStorage) CreateWorkflow(ctx context.Context, wf storage.Workflow) error { return nil }
func (a *apiStorage) DeleteWorkflow(ctx context.Context, id string) error           { return nil }

// --- Workers (unsupported mutation) ---

func (a *apiStorage) UpdateWorker(ctx context.Context, worker storage.Worker) error { return nil }

// --- Logs ---

func (a *apiStorage) ListLogs(ctx context.Context, filter storage.LogFilter) ([]storage.Log, int, error) {
	return nil, 0, nil
}
func (a *apiStorage) CreateLogs(ctx context.Context, logs []storage.Log) error { return nil }
func (a *apiStorage) DeleteLogs(ctx context.Context, filter storage.LogFilter) error {
	return nil
}
func (a *apiStorage) PurgeLogs(ctx context.Context, before time.Time) error { return nil }

// --- Audit logs ---

func (a *apiStorage) ListAuditLogs(ctx context.Context, filter storage.AuditFilter) ([]storage.AuditLog, int, error) {
	return nil, 0, nil
}
func (a *apiStorage) CreateAuditLog(ctx context.Context, log storage.AuditLog) error { return nil }
func (a *apiStorage) PurgeAuditLogs(ctx context.Context, before time.Time) error     { return nil }
func (a *apiStorage) PurgeMessageTraces(ctx context.Context, before time.Time) error { return nil }

// --- Webhook requests ---

func (a *apiStorage) ListWebhookRequests(ctx context.Context, filter storage.WebhookRequestFilter) ([]storage.WebhookRequest, int, error) {
	return nil, 0, nil
}
func (a *apiStorage) CreateWebhookRequest(ctx context.Context, req storage.WebhookRequest) error {
	return nil
}
func (a *apiStorage) GetWebhookRequest(ctx context.Context, id string) (storage.WebhookRequest, error) {
	return storage.WebhookRequest{}, storage.ErrNotFound
}
func (a *apiStorage) DeleteWebhookRequests(ctx context.Context, filter storage.WebhookRequestFilter) error {
	return nil
}

// --- Form submissions ---

func (a *apiStorage) CreateFormSubmission(ctx context.Context, sub storage.FormSubmission) error {
	return nil
}
func (a *apiStorage) ListFormSubmissions(ctx context.Context, filter storage.FormSubmissionFilter) ([]storage.FormSubmission, int, error) {
	return nil, 0, nil
}
func (a *apiStorage) GetFormSubmission(ctx context.Context, id string) (storage.FormSubmission, error) {
	return storage.FormSubmission{}, storage.ErrNotFound
}
func (a *apiStorage) UpdateFormSubmissionStatus(ctx context.Context, id, status string) error {
	return nil
}
func (a *apiStorage) DeleteFormSubmissions(ctx context.Context, filter storage.FormSubmissionFilter) error {
	return nil
}

// --- Settings ---

func (a *apiStorage) GetSetting(ctx context.Context, key string) (string, error) {
	return "", storage.ErrNotFound
}
func (a *apiStorage) SaveSetting(ctx context.Context, key, value string) error { return nil }

// --- Node state management ---

func (a *apiStorage) UpdateNodeState(ctx context.Context, workflowID, nodeID string, state any) error {
	return nil
}
func (a *apiStorage) GetNodeStates(ctx context.Context, workflowID string) (map[string]any, error) {
	return map[string]any{}, nil
}

// --- Schema registry ---

func (a *apiStorage) ListSchemas(ctx context.Context, name string) ([]storage.Schema, error) {
	return nil, nil
}
func (a *apiStorage) ListAllSchemas(ctx context.Context) ([]storage.Schema, error) {
	return nil, nil
}
func (a *apiStorage) GetSchema(ctx context.Context, name string, version int) (storage.Schema, error) {
	return storage.Schema{}, storage.ErrNotFound
}
func (a *apiStorage) GetLatestSchema(ctx context.Context, name string) (storage.Schema, error) {
	return storage.Schema{}, storage.ErrNotFound
}
func (a *apiStorage) CreateSchema(ctx context.Context, schema storage.Schema) error { return nil }

// --- Message tracing ---

func (a *apiStorage) RecordTraceStep(ctx context.Context, workflowID, messageID string, step hermod.TraceStep) error {
	return nil
}
func (a *apiStorage) GetMessageTrace(ctx context.Context, workflowID, messageID string) (storage.MessageTrace, error) {
	return storage.MessageTrace{}, storage.ErrNotFound
}
func (a *apiStorage) ListMessageTraces(ctx context.Context, workflowID string, limit int) ([]storage.MessageTrace, error) {
	return nil, nil
}

// --- Workflow versioning ---

func (a *apiStorage) CreateWorkflowVersion(ctx context.Context, version storage.WorkflowVersion) error {
	return nil
}
func (a *apiStorage) ListWorkflowVersions(ctx context.Context, workflowID string) ([]storage.WorkflowVersion, error) {
	return nil, nil
}
func (a *apiStorage) GetWorkflowVersion(ctx context.Context, workflowID string, version int) (storage.WorkflowVersion, error) {
	return storage.WorkflowVersion{}, storage.ErrNotFound
}

// --- Transactional outbox ---

func (a *apiStorage) CreateOutboxItem(ctx context.Context, item storage.OutboxItem) error {
	return nil
}
func (a *apiStorage) ListOutboxItems(ctx context.Context, status string, limit int) ([]storage.OutboxItem, error) {
	return nil, nil
}
func (a *apiStorage) DeleteOutboxItem(ctx context.Context, id string) error { return nil }
func (a *apiStorage) UpdateOutboxItem(ctx context.Context, item storage.OutboxItem) error {
	return nil
}

// --- Lineage ---

func (a *apiStorage) GetLineage(ctx context.Context) ([]storage.LineageEdge, error) {
	return nil, nil
}

// --- Marketplace ---

func (a *apiStorage) ListPlugins(ctx context.Context) ([]storage.Plugin, error) { return nil, nil }
func (a *apiStorage) GetPlugin(ctx context.Context, id string) (storage.Plugin, error) {
	return storage.Plugin{}, storage.ErrNotFound
}
func (a *apiStorage) InstallPlugin(ctx context.Context, id string) error   { return nil }
func (a *apiStorage) UninstallPlugin(ctx context.Context, id string) error { return nil }

// --- Approvals ---

func (a *apiStorage) ListApprovals(ctx context.Context, filter storage.ApprovalFilter) ([]storage.Approval, int, error) {
	return nil, 0, nil
}
func (a *apiStorage) CreateApproval(ctx context.Context, app storage.Approval) error { return nil }
func (a *apiStorage) GetApproval(ctx context.Context, id string) (storage.Approval, error) {
	return storage.Approval{}, storage.ErrNotFound
}
func (a *apiStorage) UpdateApprovalStatus(ctx context.Context, id, status, processedBy, notes string, formData map[string]any) error {
	return nil
}
func (a *apiStorage) DeleteApproval(ctx context.Context, id string) error { return nil }

// --- Suspended messages ---

func (a *apiStorage) CreateSuspendedMessage(ctx context.Context, m storage.SuspendedMessage) error {
	return nil
}
func (a *apiStorage) ListSuspendedMessages(ctx context.Context, workflowID string, before time.Time) ([]storage.SuspendedMessage, error) {
	return nil, nil
}
func (a *apiStorage) DeleteSuspendedMessage(ctx context.Context, id string) error { return nil }
