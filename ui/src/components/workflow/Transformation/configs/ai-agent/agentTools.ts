/**
 * What the ai_agent editor knows about the node's tools and limits. Every rule
 * mirrors internal/engine/registry/nodes/ai/agent/config.go (what the engine
 * accepts) and agentNodeIssues in workflow_validation_ai.go (what workflow
 * validation reports), so the form flags what the backend would.
 */
import { isPlaintextKey } from '../ai/aiProviders'
import { isMissing, type NodeIssue } from '../ai-retrieve/retrieveIssues'

/** Read kinds wrap a lookup transformer with fixed config (config.go ReadKinds). */
export const READ_KINDS = ['db_lookup', 'api_lookup', 'ai_retrieve'] as const
export const SINK_KIND = 'sink'

export const TOOL_KINDS = [
  { value: 'db_lookup', label: 'Database lookup (db_lookup)' },
  { value: 'api_lookup', label: 'API call (api_lookup)' },
  { value: 'ai_retrieve', label: 'Vector search (ai_retrieve)' },
  { value: SINK_KIND, label: 'Write to a sink node' },
]

/** JSON Schema types a parameter may declare (config.go paramTypes). */
export const PARAM_TYPES = ['string', 'number', 'integer', 'boolean'] as const

/** Defaults and ceilings the node cannot raise (config.go). */
export const AGENT_LIMITS = {
  maxSteps: { default: 5, cap: 20 },
  maxTotalTokens: { default: 50_000, cap: 1_000_000 },
  timeout: { default: '2m', capMs: 10 * 60_000, cap: '10m' },
} as const

/** agent.go DefaultTargetField / DefaultTranscriptField / PendingCallField. */
export const AGENT_FIELDS = {
  target: 'ai_agent_answer',
  transcript: 'ai_agent_transcript',
  pendingCall: 'ai_agent_pending_tool_call',
} as const

/** The field a read tool's lookup is told to write to; the node forces it. */
export const TOOL_RESULT_FIELD = 'result'

const TOOL_NAME = /^[A-Za-z0-9_-]{1,64}$/

export interface AgentParam {
  name: string
  type?: string
  description?: string
  required?: boolean
}

export interface AgentTool {
  name: string
  description?: string
  kind?: string
  parameters?: AgentParam[]
  /** A read tool's fixed transformer config. */
  config?: Record<string, unknown>
  /** The sink node a sink tool writes to. */
  nodeId?: string
  write?: boolean
  requireApproval?: boolean
}

export interface AgentIssue extends NodeIssue {
  /** The tool the issue is about, by position. */
  toolIndex?: number
}

/** A workflow node as the editor's store holds it. */
export interface WorkflowNodeLike {
  id: string
  type?: string
  data?: Record<string, any>
}

/** The tools, given as a list or as its JSON text (config.go parseTools). */
export function toolList(raw: unknown): { tools: AgentTool[]; error?: string } {
  let value = raw
  if (typeof raw === 'string') {
    if (raw.trim() === '') return { tools: [] }
    try {
      value = JSON.parse(raw)
    } catch (e) {
      return { tools: [], error: `The tools are not valid JSON: ${(e as Error).message}` }
    }
  }
  return { tools: Array.isArray(value) ? (value as AgentTool[]) : [] }
}

const isReadKind = (kind: unknown) => (READ_KINDS as readonly unknown[]).includes(kind)

/** A sink always writes; a read kind only when marked (config.go parseTool). */
export const isWriteTool = (t: AgentTool) => t.kind === SINK_KIND || (isReadKind(t.kind) && t.write === true)

/** Approval is on for a write tool unless requireApproval is exactly false. */
export const needsApproval = (t: AgentTool) => isWriteTool(t) && t.requireApproval !== false

/** The workflow's sink nodes, which a sink tool may write to. */
export const sinkNodes = (nodes: WorkflowNodeLike[]) => nodes.filter((n) => n.type === 'sink')

/** Milliseconds in a Go duration such as 90s or 1m30s, or NaN. */
export function goDurationMs(raw: string): number {
  const s = raw.trim()
  if (!/^(\d+(\.\d+)?(ns|us|µs|ms|s|m|h))+$/.test(s)) return NaN
  const unit: Record<string, number> = { ns: 1e-6, us: 1e-3, µs: 1e-3, ms: 1, s: 1000, m: 60_000, h: 3_600_000 }
  let total = 0
  for (const [, n, , u] of s.matchAll(/(\d+(\.\d+)?)(ns|us|µs|ms|s|m|h)/g)) total += Number(n) * unit[u]
  return total
}

function toolIssues(t: unknown, i: number, seen: Set<string>, sinks: Set<string>): AgentIssue[] {
  const out: AgentIssue[] = []
  const err = (message: string, field: string) => out.push({ severity: 'error', message, toolIndex: i, field })
  if (!t || typeof t !== 'object' || Array.isArray(t)) {
    err(`Tool ${i + 1} is not an object.`, 'name')
    return out
  }
  const tool = t as AgentTool
  const name = typeof tool.name === 'string' ? tool.name.trim() : ''
  if (!TOOL_NAME.test(name)) {
    err(`Tool name "${name}" must be 1-64 letters, digits, _ or -.`, 'name')
  } else if (seen.has(name)) {
    err(`Tool name "${name}" is used twice.`, 'name')
  }
  seen.add(name)

  const kind = typeof tool.kind === 'string' ? tool.kind.trim() : ''
  if (kind === SINK_KIND) {
    const nodeId = typeof tool.nodeId === 'string' ? tool.nodeId.trim() : ''
    if (!nodeId) err(`Pick the sink node tool "${name}" writes to.`, 'nodeId')
    else if (!sinks.has(nodeId)) err(`Tool "${name}" writes to node "${nodeId}", which is not a sink node of this workflow.`, 'nodeId')
  } else if (!isReadKind(kind)) {
    err(`Tool "${name}" needs a kind: a lookup, an API call, a vector search or a sink.`, 'kind')
  }

  const params = Array.isArray(tool.parameters) ? tool.parameters : []
  const names = new Set<string>()
  for (const p of params) {
    const pname = p && typeof p.name === 'string' ? p.name.trim() : ''
    const ptype = (p && typeof p.type === 'string' ? p.type.trim() : '') || 'string'
    if (!TOOL_NAME.test(pname) || names.has(pname)) {
      err(`Tool "${name}" has a parameter with a missing, invalid or repeated name.`, 'parameters')
    } else if (!(PARAM_TYPES as readonly string[]).includes(ptype)) {
      err(`Tool "${name}" parameter "${pname}" has type "${ptype}"; use string, number, integer or boolean.`, 'parameters')
    }
    names.add(pname)
  }

  if (isWriteTool({ ...tool, kind }) && tool.requireApproval === false) {
    out.push({ severity: 'warning', message: `Tool "${name}" can write without anyone approving it.`, toolIndex: i, field: 'requireApproval' })
  }
  return out
}

/**
 * Everything wrong with an ai_agent node: what workflow validation reports
 * (aiNodeIssues + agentNodeIssues), plus tool definitions the engine refuses
 * at run time, which would fail every message.
 */
export function agentIssues(config: Record<string, unknown>, nodes: WorkflowNodeLike[]): AgentIssue[] {
  if (isMissing(config.provider)) {
    return [{ severity: 'error', message: 'Choose the AI provider the agent runs on.', field: 'provider' }]
  }
  const issues: AgentIssue[] = []
  if (isMissing(config.goal) && isMissing(config.prompt)) {
    issues.push({ severity: 'error', message: 'The agent needs a goal.', field: 'goal' })
  }
  const { tools, error } = toolList(config.tools)
  if (error) {
    issues.push({ severity: 'error', message: error, field: 'tools' })
  } else if (tools.length === 0) {
    issues.push({ severity: 'error', message: 'The agent needs at least one tool.', field: 'tools' })
  } else {
    const seen = new Set<string>()
    const sinks = new Set(sinkNodes(nodes).map((n) => n.id))
    tools.forEach((t, i) => issues.push(...toolIssues(t, i, seen, sinks)))
  }
  const provider = String(config.provider).trim().toLowerCase()
  if (isPlaintextKey(config.apiKey) && provider !== 'ollama' && provider !== 'openai_compatible') {
    issues.push({
      severity: 'warning',
      message: 'The API key is stored in the workflow. Use a vhost secret instead.',
      field: 'apiKey',
    })
  }
  return issues
}
