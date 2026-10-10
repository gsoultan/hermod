/**
 * The ai_agent node's run record, as nodes/ai/agent/transcript.go writes it
 * onto the message (under the node's transcriptField, ai_agent_transcript by
 * default), and the pending call approval.go adds for a reviewer.
 */

export interface TranscriptEntry {
  step: number
  /** model, tool_call, tool_result, approval_requested, approval_decision */
  kind: string
  text?: string
  tool?: string
  call_id?: string
  input?: string
  is_error?: boolean
  stop_reason?: string
  input_tokens?: number
  output_tokens?: number
}

export interface AgentTranscript {
  /** completed, failed or awaiting_approval */
  status: string
  steps?: number
  usage?: { input_tokens?: number; output_tokens?: number }
  entries: TranscriptEntry[]
  truncated?: boolean
  error?: string
}

export interface PendingToolCall {
  tool: string
  description?: string
  call_id?: string
  arguments?: Record<string, unknown>
}

/** agent.go PendingCallField. */
export const PENDING_CALL_FIELD = 'ai_agent_pending_tool_call'

const STATUSES = new Set(['completed', 'failed', 'awaiting_approval'])

const isObject = (v: unknown): v is Record<string, any> => !!v && typeof v === 'object' && !Array.isArray(v)

/** Whether a value is a transcript. The field is configurable, so it is told by its shape. */
export function isTranscript(v: unknown): v is AgentTranscript {
  return isObject(v) && STATUSES.has(v.status) && Array.isArray(v.entries)
}

/** Every transcript among a message's top-level fields. */
export function findTranscripts(data: unknown): { field: string; transcript: AgentTranscript }[] {
  if (!isObject(data)) return []
  return Object.entries(data)
    .filter(([, v]) => isTranscript(v))
    .map(([field, transcript]) => ({ field, transcript: transcript as AgentTranscript }))
}

/** The tool call an ai_agent approval is for, or null. */
export function pendingToolCall(data: unknown): PendingToolCall | null {
  const v = isObject(data) ? data[PENDING_CALL_FIELD] : undefined
  return isObject(v) && typeof v.tool === 'string' ? (v as PendingToolCall) : null
}
