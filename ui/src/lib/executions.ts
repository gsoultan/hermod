import { apiFetch } from '@/api'

/**
 * A workflow's run history, read from internal/executions/transport/http.
 *
 * A run is one triggering message: its id is the message's trace id, so the
 * id a manual run or a replay answers with opens here.
 */

/** Language-model usage, as genai.Usage serialises it. */
export interface AIUsage {
  calls: number
  input_tokens: number
  output_tokens: number
}

export type ExecutionStatus = 'succeeded' | 'failed' | 'waiting'

/** One node's part in a run (executions.Step). */
export interface ExecutionStep {
  node_id: string
  node_type?: string
  label?: string
  timestamp: string
  duration_ms: number
  error?: string
  ai?: AIUsage
  /** The message as the step left it. Only on a single run, never in the list. */
  output?: Record<string, unknown>
}

/** One run of a workflow (executions.Execution). */
export interface Execution {
  run_id: string
  workflow_id: string
  started_at: string
  duration_ms: number
  status: ExecutionStatus
  step_count: number
  error_count: number
  ai?: AIUsage
  steps?: ExecutionStep[]
}

export interface ExecutionPage {
  executions: Execution[]
  /** Cursor for the next, older page; absent on the last page. */
  next_before?: string
}

/** A step as RunWorkflowOnce reports it (hermod.TraceStep): duration in nanoseconds. */
export interface RunStep {
  node_id: string
  timestamp?: string
  duration?: number
  before?: Record<string, unknown>
  after?: Record<string, unknown>
  error?: string
}

/** registry.RunStatus: how a manual run or a replay ended. */
export type RunStatus = 'completed' | 'failed' | 'waiting'

export interface RunResult {
  workflow_id: string
  run_id: string
  status: RunStatus
  steps: RunStep[] | null
}

export interface ReplayResult extends RunResult {
  replay_of: string
}

/** The server's own words for a refused request, and its HTTP status. */
export class ApiRequestError extends Error {
  readonly status: number
  constructor(message: string, status: number) {
    super(message)
    this.name = 'ApiRequestError'
    this.status = status
  }
}

/**
 * Calls apiFetch without its global toast and rethrows with the server's
 * message and status, so the screen that made the request can say what went
 * wrong where it went wrong.
 */
export async function requestJson<T>(url: string, init: RequestInit = {}): Promise<T> {
  let res: Response
  try {
    res = await apiFetch(url, {
      ...init,
      headers: init.body ? { 'Content-Type': 'application/json', ...(init.headers || {}) } : init.headers,
      silent: true,
    })
  } catch (err: any) {
    throw new ApiRequestError(err?.message || 'The request failed.', typeof err?.status === 'number' ? err.status : 0)
  }
  return (await res.json()) as T
}

const base = (workflowId: string) => `/api/workflows/${encodeURIComponent(workflowId)}`

export const executionsKey = (workflowId: string) => ['executions', workflowId] as const

export function listExecutions(
  workflowId: string,
  opts: { before?: string | null; limit?: number } = {},
  signal?: AbortSignal,
): Promise<ExecutionPage> {
  const params = new URLSearchParams({ limit: String(opts.limit ?? 20) })
  if (opts.before) params.set('before', opts.before)
  return requestJson<ExecutionPage>(`${base(workflowId)}/executions?${params.toString()}`, { signal })
}

export function getExecution(workflowId: string, runId: string, signal?: AbortSignal): Promise<Execution> {
  return requestJson<Execution>(`${base(workflowId)}/executions/${encodeURIComponent(runId)}`, { signal })
}

export function replayExecution(workflowId: string, runId: string): Promise<ReplayResult> {
  return requestJson<ReplayResult>(`${base(workflowId)}/executions/${encodeURIComponent(runId)}/replay`, {
    method: 'POST',
  })
}

/**
 * Runs the workflow once with message as its source's output: POST
 * /api/workflows/{id}/run. It is a real run -- sinks write -- and it answers
 * when the run has finished or is held at an approval.
 */
export function runWorkflow(workflowId: string, message: Record<string, unknown>, sourceNodeId?: string): Promise<RunResult> {
  const body: { message: Record<string, unknown>; source_node_id?: string } = { message }
  if (sourceNodeId) body.source_node_id = sourceNodeId
  return requestJson<RunResult>(`${base(workflowId)}/run`, { method: 'POST', body: JSON.stringify(body) })
}

/** "1.25 s", "40 ms". Zero is a real reading here: a run that took no time. */
export function formatDuration(ms: number | undefined | null): string {
  if (typeof ms !== 'number' || !Number.isFinite(ms) || ms < 0) return '—'
  if (ms >= 60_000) {
    const m = Math.floor(ms / 60_000)
    const s = Math.round((ms % 60_000) / 1000)
    return `${m}m ${s}s`
  }
  if (ms >= 1000) return `${(ms / 1000).toFixed(2)} s`
  return `${Math.round(ms)} ms`
}

/** "120 in · 30 out", or a dash when the run made no model calls. */
export function formatTokens(ai: AIUsage | undefined | null): string {
  if (!ai || (!ai.input_tokens && !ai.output_tokens)) return '—'
  return `${ai.input_tokens.toLocaleString()} in · ${ai.output_tokens.toLocaleString()} out`
}

export const executionStatusMeta: Record<string, { label: string; color: string }> = {
  succeeded: { label: 'Succeeded', color: 'green' },
  completed: { label: 'Completed', color: 'green' },
  failed: { label: 'Failed', color: 'red' },
  waiting: { label: 'Waiting', color: 'yellow' },
}

export function statusMeta(status: string): { label: string; color: string } {
  return executionStatusMeta[status] ?? { label: status || 'Unknown', color: 'gray' }
}
