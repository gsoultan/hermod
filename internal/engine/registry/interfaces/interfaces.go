package interfaces

import (
	"context"
	"sync"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
)

type RegistryStorage interface {
	GetSource(ctx context.Context, id string) (storage.Source, error)
	GetSink(ctx context.Context, id string) (storage.Sink, error)
	GetWorkflow(ctx context.Context, id string) (storage.Workflow, error)
	ListWorkflows(ctx context.Context, filter storage.CommonFilter) ([]storage.Workflow, int, error)
	ListSources(ctx context.Context, filter storage.CommonFilter) ([]storage.Source, int, error)
	ListSinks(ctx context.Context, filter storage.CommonFilter) ([]storage.Sink, int, error)
	ListWorkers(ctx context.Context, filter storage.CommonFilter) ([]storage.Worker, int, error)

	CreateFormSubmission(ctx context.Context, sub storage.FormSubmission) error
	ListFormSubmissions(ctx context.Context, filter storage.FormSubmissionFilter) ([]storage.FormSubmission, int, error)
	UpdateFormSubmissionStatus(ctx context.Context, id string, status string) error
	UpdateWorkflow(ctx context.Context, wf storage.Workflow) error
	UpdateWorkflowStatus(ctx context.Context, id string, status string) error
	UpdateWorkflowStats(ctx context.Context, id string, processed, errors, lag uint64) error
	UpdateSourceStatus(ctx context.Context, id string, status string) error
	UpdateSinkStatus(ctx context.Context, id string, status string) error
	CreateLog(ctx context.Context, log storage.Log) error
	CreateLogs(ctx context.Context, logs []storage.Log) error
	DeleteLogs(ctx context.Context, filter storage.LogFilter) error
	UpdateSource(ctx context.Context, src storage.Source) error
	UpdateSourceState(ctx context.Context, id string, state map[string]string) error
	UpdateSink(ctx context.Context, snk storage.Sink) error
	UpdateNodeState(ctx context.Context, workflowID, nodeID string, state any) error
	GetNodeStates(ctx context.Context, workflowID string) (map[string]any, error)
	RecordTraceStep(ctx context.Context, workflowID, messageID string, step hermod.TraceStep) error
	PurgeLogs(ctx context.Context, before time.Time) error
	PurgeAuditLogs(ctx context.Context, before time.Time) error
	PurgeMessageTraces(ctx context.Context, before time.Time) error

	CreateApproval(ctx context.Context, app storage.Approval) error
	GetApproval(ctx context.Context, id string) (storage.Approval, error)
	UpdateApprovalStatus(ctx context.Context, id string, status string, processedBy string, notes string, formData map[string]any) error

	CreateSuspendedMessage(ctx context.Context, m storage.SuspendedMessage) error
	ListSuspendedMessages(ctx context.Context, workflowID string, before time.Time) ([]storage.SuspendedMessage, error)
	DeleteSuspendedMessage(ctx context.Context, id string) error

	GetDashboardStats(ctx context.Context, vhost string) (storage.DashboardStats, error)
}

type NodeContext interface {
	BroadcastLiveMessage(workflowID, nodeID string, msg hermod.Message, isError bool, errMsg string)
	BroadcastLog(workflowID, level, msg, msgID string)
	ApplyTransformation(ctx context.Context, msg hermod.Message, transType string, config map[string]any) (hermod.Message, error)
	ContextWithPipelineSnapshot(ctx context.Context) context.Context
	EvaluateConditions(msg hermod.Message, conditions []map[string]any) bool
	Storage() RegistryStorage
	StateStore() hermod.StateStore
	GetNodeState(key string) (any, bool)
	SetNodeState(key string, val any)
	GetSink(workflowID, nodeID string) (hermod.Sink, bool)
}

type NodeExecutor interface {
	Execute(ctx context.Context, nctx NodeContext, workflowID string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error)
}

var (
	executorsMu sync.RWMutex
	executors   = make(map[string]NodeExecutor)
)

// RegisterNodeExecutor registers a node executor for a given node type.
func RegisterNodeExecutor(nodeType string, executor NodeExecutor) {
	executorsMu.Lock()
	defer executorsMu.Unlock()
	executors[nodeType] = executor
}

// GetNodeExecutor retrieves a node executor for a given node type.
func GetNodeExecutor(nodeType string) (NodeExecutor, bool) {
	executorsMu.RLock()
	defer executorsMu.RUnlock()
	e, ok := executors[nodeType]
	return e, ok
}

// PreviewSafe is implemented by node executors that may be run against a sample
// message in the workflow editor.
//
// The editor's "Test" button needs to run a node outside a workflow, and most
// nodes must not be run that way: a sink writes to the destination for real, an
// approval records a request somebody has to answer, a stateful node mutates
// state a running workflow depends on. Pressing a button in an editor is not
// consent to any of that.
//
// Implementing this is a claim about the executor: given a message and its own
// config it decides an outcome, touching no storage, no state store, no sink,
// and broadcasting nothing. The routing nodes qualify because deciding a branch
// is all they do.
//
// It is a marker rather than something inferred from the executor's behaviour,
// for the same reason tombstone tags are listed rather than detected: a
// property worth trusting should be declared, not guessed at from a signal that
// happens to correlate today.
type PreviewSafe interface {
	NodeExecutor

	// PreviewSafeNode is a marker. It does nothing and is never called.
	PreviewSafeNode()
}

// GetPreviewSafeExecutor returns the executor registered for nodeType if it has
// declared itself safe to run in a preview. A node that exists but has not
// declared it returns false, which callers should report differently from a
// node that does not exist at all -- one is a limitation, the other a typo.
func GetPreviewSafeExecutor(nodeType string) (NodeExecutor, bool) {
	e, ok := GetNodeExecutor(nodeType)
	if !ok {
		return nil, false
	}
	if _, safe := e.(PreviewSafe); !safe {
		return nil, false
	}
	return e, true
}
