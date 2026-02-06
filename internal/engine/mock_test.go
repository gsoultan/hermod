package engine

import (
	"context"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/storage"
)

type BaseMockStorage struct {
	storage.Storage
}

func (m *BaseMockStorage) Init(ctx context.Context) error { return nil }

func (m *BaseMockStorage) ListSources(ctx context.Context, filter storage.CommonFilter) ([]storage.Source, int, error) {
	return nil, 0, nil
}
func (m *BaseMockStorage) CreateSource(ctx context.Context, src storage.Source) error { return nil }
func (m *BaseMockStorage) UpdateSource(ctx context.Context, src storage.Source) error { return nil }
func (m *BaseMockStorage) UpdateSourceStatus(ctx context.Context, id string, status string) error {
	return nil
}
func (m *BaseMockStorage) UpdateSourceState(ctx context.Context, id string, state map[string]string) error {
	return nil
}
func (m *BaseMockStorage) DeleteSource(ctx context.Context, id string) error { return nil }
func (m *BaseMockStorage) GetSource(ctx context.Context, id string) (storage.Source, error) {
	return storage.Source{}, storage.ErrNotFound
}

func (m *BaseMockStorage) ListSinks(ctx context.Context, filter storage.CommonFilter) ([]storage.Sink, int, error) {
	return nil, 0, nil
}
func (m *BaseMockStorage) CreateSink(ctx context.Context, snk storage.Sink) error { return nil }
func (m *BaseMockStorage) UpdateSink(ctx context.Context, snk storage.Sink) error { return nil }
func (m *BaseMockStorage) UpdateSinkStatus(ctx context.Context, id string, status string) error {
	return nil
}
func (m *BaseMockStorage) DeleteSink(ctx context.Context, id string) error { return nil }
func (m *BaseMockStorage) GetSink(ctx context.Context, id string) (storage.Sink, error) {
	return storage.Sink{}, storage.ErrNotFound
}

func (m *BaseMockStorage) ListUsers(ctx context.Context, filter storage.CommonFilter) ([]storage.User, int, error) {
	return nil, 0, nil
}
func (m *BaseMockStorage) CreateUser(ctx context.Context, user storage.User) error { return nil }
func (m *BaseMockStorage) UpdateUser(ctx context.Context, user storage.User) error { return nil }
func (m *BaseMockStorage) DeleteUser(ctx context.Context, id string) error         { return nil }
func (m *BaseMockStorage) GetUser(ctx context.Context, id string) (storage.User, error) {
	return storage.User{}, storage.ErrNotFound
}
func (m *BaseMockStorage) GetUserByUsername(ctx context.Context, username string) (storage.User, error) {
	return storage.User{}, storage.ErrNotFound
}
func (m *BaseMockStorage) GetUserByEmail(ctx context.Context, email string) (storage.User, error) {
	return storage.User{}, storage.ErrNotFound
}

func (m *BaseMockStorage) ListVHosts(ctx context.Context, filter storage.CommonFilter) ([]storage.VHost, int, error) {
	return nil, 0, nil
}
func (m *BaseMockStorage) CreateVHost(ctx context.Context, vhost storage.VHost) error { return nil }
func (m *BaseMockStorage) DeleteVHost(ctx context.Context, id string) error           { return nil }
func (m *BaseMockStorage) GetVHost(ctx context.Context, id string) (storage.VHost, error) {
	return storage.VHost{}, storage.ErrNotFound
}

func (m *BaseMockStorage) ListWorkflows(ctx context.Context, filter storage.CommonFilter) ([]storage.Workflow, int, error) {
	return nil, 0, nil
}
func (m *BaseMockStorage) ListWorkspaces(ctx context.Context) ([]storage.Workspace, error) {
	return nil, nil
}
func (m *BaseMockStorage) CreateWorkspace(ctx context.Context, ws storage.Workspace) error {
	return nil
}
func (m *BaseMockStorage) GetWorkspace(ctx context.Context, id string) (storage.Workspace, error) {
	return storage.Workspace{}, storage.ErrNotFound
}
func (m *BaseMockStorage) DeleteWorkspace(ctx context.Context, id string) error          { return nil }
func (m *BaseMockStorage) CreateWorkflow(ctx context.Context, wf storage.Workflow) error { return nil }
func (m *BaseMockStorage) UpdateWorkflow(ctx context.Context, wf storage.Workflow) error { return nil }
func (m *BaseMockStorage) UpdateWorkflowStatus(ctx context.Context, id string, status string) error {
	return nil
}
func (m *BaseMockStorage) DeleteWorkflow(ctx context.Context, id string) error { return nil }
func (m *BaseMockStorage) GetWorkflow(ctx context.Context, id string) (storage.Workflow, error) {
	return storage.Workflow{}, storage.ErrNotFound
}

func (m *BaseMockStorage) AcquireWorkflowLease(ctx context.Context, workflowID, ownerID string, ttlSeconds int) (bool, error) {
	return true, nil
}
func (m *BaseMockStorage) RenewWorkflowLease(ctx context.Context, workflowID, ownerID string, ttlSeconds int) (bool, error) {
	return true, nil
}
func (m *BaseMockStorage) ReleaseWorkflowLease(ctx context.Context, workflowID, ownerID string) error {
	return nil
}

func (m *BaseMockStorage) ListWorkers(ctx context.Context, filter storage.CommonFilter) ([]storage.Worker, int, error) {
	return nil, 0, nil
}
func (m *BaseMockStorage) CreateWorker(ctx context.Context, worker storage.Worker) error { return nil }
func (m *BaseMockStorage) UpdateWorker(ctx context.Context, worker storage.Worker) error { return nil }
func (m *BaseMockStorage) UpdateWorkerHeartbeat(ctx context.Context, id string, cpu, mem float64) error {
	return nil
}
func (m *BaseMockStorage) DeleteWorker(ctx context.Context, id string) error { return nil }
func (m *BaseMockStorage) GetWorker(ctx context.Context, id string) (storage.Worker, error) {
	return storage.Worker{}, storage.ErrNotFound
}

func (m *BaseMockStorage) ListLogs(ctx context.Context, filter storage.LogFilter) ([]storage.Log, int, error) {
	return nil, 0, nil
}
func (m *BaseMockStorage) CreateLog(ctx context.Context, log storage.Log) error { return nil }
func (m *BaseMockStorage) DeleteLogs(ctx context.Context, filter storage.LogFilter) error {
	return nil
}

func (m *BaseMockStorage) ListAuditLogs(ctx context.Context, filter storage.AuditFilter) ([]storage.AuditLog, int, error) {
	return nil, 0, nil
}
func (m *BaseMockStorage) CreateAuditLog(ctx context.Context, log storage.AuditLog) error { return nil }
func (m *BaseMockStorage) PurgeAuditLogs(ctx context.Context, before time.Time) error     { return nil }
func (m *BaseMockStorage) PurgeMessageTraces(ctx context.Context, before time.Time) error {
	return nil
}

func (m *BaseMockStorage) ListWebhookRequests(ctx context.Context, filter storage.WebhookRequestFilter) ([]storage.WebhookRequest, int, error) {
	return nil, 0, nil
}
func (m *BaseMockStorage) CreateWebhookRequest(ctx context.Context, req storage.WebhookRequest) error {
	return nil
}
func (m *BaseMockStorage) GetWebhookRequest(ctx context.Context, id string) (storage.WebhookRequest, error) {
	return storage.WebhookRequest{}, storage.ErrNotFound
}
func (m *BaseMockStorage) DeleteWebhookRequests(ctx context.Context, filter storage.WebhookRequestFilter) error {
	return nil
}

func (m *BaseMockStorage) CreateFormSubmission(ctx context.Context, sub storage.FormSubmission) error {
	return nil
}
func (m *BaseMockStorage) ListFormSubmissions(ctx context.Context, filter storage.FormSubmissionFilter) ([]storage.FormSubmission, int, error) {
	return nil, 0, nil
}
func (m *BaseMockStorage) GetFormSubmission(ctx context.Context, id string) (storage.FormSubmission, error) {
	return storage.FormSubmission{}, storage.ErrNotFound
}
func (m *BaseMockStorage) UpdateFormSubmissionStatus(ctx context.Context, id string, status string) error {
	return nil
}
func (m *BaseMockStorage) DeleteFormSubmissions(ctx context.Context, filter storage.FormSubmissionFilter) error {
	return nil
}

func (m *BaseMockStorage) GetSetting(ctx context.Context, key string) (string, error) { return "", nil }
func (m *BaseMockStorage) SaveSetting(ctx context.Context, key string, value string) error {
	return nil
}

func (m *BaseMockStorage) UpdateNodeState(ctx context.Context, workflowID, nodeID string, state interface{}) error {
	return nil
}
func (m *BaseMockStorage) GetNodeStates(ctx context.Context, workflowID string) (map[string]interface{}, error) {
	return nil, nil
}

func (m *BaseMockStorage) ListSchemas(ctx context.Context, name string) ([]storage.Schema, error) {
	return nil, nil
}
func (m *BaseMockStorage) ListAllSchemas(ctx context.Context) ([]storage.Schema, error) {
	return nil, nil
}
func (m *BaseMockStorage) GetSchema(ctx context.Context, name string, version int) (storage.Schema, error) {
	return storage.Schema{}, storage.ErrNotFound
}
func (m *BaseMockStorage) GetLatestSchema(ctx context.Context, name string) (storage.Schema, error) {
	return storage.Schema{}, storage.ErrNotFound
}
func (m *BaseMockStorage) CreateSchema(ctx context.Context, schema storage.Schema) error { return nil }

func (m *BaseMockStorage) RecordTraceStep(ctx context.Context, workflowID, messageID string, step hermod.TraceStep) error {
	return nil
}
func (m *BaseMockStorage) GetMessageTrace(ctx context.Context, workflowID, messageID string) (storage.MessageTrace, error) {
	return storage.MessageTrace{}, storage.ErrNotFound
}
func (m *BaseMockStorage) ListMessageTraces(ctx context.Context, workflowID string, limit int) ([]storage.MessageTrace, error) {
	return nil, nil
}

func (m *BaseMockStorage) CreateWorkflowVersion(ctx context.Context, version storage.WorkflowVersion) error {
	return nil
}
func (m *BaseMockStorage) ListWorkflowVersions(ctx context.Context, workflowID string) ([]storage.WorkflowVersion, error) {
	return nil, nil
}
func (m *BaseMockStorage) GetWorkflowVersion(ctx context.Context, workflowID string, version int) (storage.WorkflowVersion, error) {
	return storage.WorkflowVersion{}, storage.ErrNotFound
}

func (m *BaseMockStorage) CreateOutboxItem(ctx context.Context, item storage.OutboxItem) error {
	return nil
}
func (m *BaseMockStorage) ListOutboxItems(ctx context.Context, status string, limit int) ([]storage.OutboxItem, error) {
	return nil, nil
}
func (m *BaseMockStorage) DeleteOutboxItem(ctx context.Context, id string) error { return nil }
func (m *BaseMockStorage) UpdateOutboxItem(ctx context.Context, item storage.OutboxItem) error {
	return nil
}

func (m *BaseMockStorage) GetLineage(ctx context.Context) ([]storage.LineageEdge, error) {
	return nil, nil
}

func (m *BaseMockStorage) ListPlugins(ctx context.Context) ([]storage.Plugin, error) { return nil, nil }
func (m *BaseMockStorage) GetPlugin(ctx context.Context, id string) (storage.Plugin, error) {
	return storage.Plugin{}, storage.ErrNotFound
}
func (m *BaseMockStorage) InstallPlugin(ctx context.Context, id string) error   { return nil }
func (m *BaseMockStorage) UninstallPlugin(ctx context.Context, id string) error { return nil }
