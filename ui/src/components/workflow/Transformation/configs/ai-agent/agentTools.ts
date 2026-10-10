/**
 * What the ai_agent editor knows about the node's tools and limits. Every rule
 * mirrors internal/engine/registry/nodes/ai/agent/config.go and
 * .../agent/mcptool/server.go (what the engine accepts) and agentNodeIssues /
 * mcpToolIssues in workflow_validation_ai.go (what workflow validation
 * reports), so the form flags what the backend would.
 */
import { isPlaintextKey } from '../ai/aiProviders'
import { isMissing, type NodeIssue } from '../ai-retrieve/retrieveIssues'

/** Read kinds wrap a lookup transformer with fixed config (config.go ReadKinds). */
export const READ_KINDS = ['db_lookup', 'api_lookup', 'ai_retrieve'] as const
export const SINK_KIND = 'sink'
/** One named tool of a remote MCP server (config.go kindMCP). */
export const MCP_KIND = 'mcp'

export const TOOL_KINDS = [
  { value: 'db_lookup', label: 'Database lookup (db_lookup)' },
  { value: 'api_lookup', label: 'API call (api_lookup)' },
  { value: 'ai_retrieve', label: 'Vector search (ai_retrieve)' },
  { value: SINK_KIND, label: 'Write to a sink node' },
  { value: MCP_KIND, label: 'Remote MCP server tool (mcp)' },
]

/**
 * Headers the MCP transport sets itself; a configured one is refused
 * (server.go reservedHeaders), compared in canonical form.
 */
export const RESERVED_HEADERS = [
  'Accept',
  'Content-Type',
  'Content-Length',
  'Host',
  'Mcp-Session-Id',
  'Mcp-Protocol-Version',
  'Last-Event-Id',
  'Connection',
  'Transfer-Encoding',
] as const

/** The header value the editor suggests, so a credential comes from a secret. */
export const SECRET_HEADER_HINT = '{{secret("NAME")}}'

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
  /** An mcp tool's server: url and header values are templates. */
  server?: McpServer
  /** The remote tool an mcp tool calls. */
  tool?: string
  write?: boolean
  requireApproval?: boolean
}

export interface McpServer {
  url?: string
  headers?: Record<string, string>
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

/**
 * A sink always writes; a read kind only when marked (config.go parseTool).
 * An mcp tool counts as one: it is spared approval only at run time, when the
 * workflow says write: false and the server marks it read-only (useRemote).
 */
export const isWriteTool = (t: AgentTool) =>
  t.kind === SINK_KIND || t.kind === MCP_KIND || (isReadKind(t.kind) && t.write === true)

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

/** Go's http.CanonicalHeaderKey for a valid token: x-api-key -> X-Api-Key. */
export const canonicalHeader = (name: string) =>
  name
    .trim()
    .split('-')
    .map((p) => p.charAt(0).toUpperCase() + p.slice(1).toLowerCase())
    .join('-')

/** An RFC 9110 token (server.go validHeaderName). */
export const validHeaderName = (name: string) => /^[!#$%&'*+\-.^_`|~0-9A-Za-z]+$/.test(name)

export const isReservedHeader = (name: string) =>
  (RESERVED_HEADERS as readonly string[]).includes(canonicalHeader(name))

/** No control characters but tab (server.go validHeaderValue). */
const validHeaderValue = (v: string) =>
  [...v].every((c) => {
    const code = c.charCodeAt(0)
    return (code >= 0x20 || code === 0x09) && code !== 0x7f
  })

/** A header that looks like it carries a credential (credentialHeader). */
export function isCredentialHeader(name: string): boolean {
  const n = name.toLowerCase()
  if (n === 'authorization' || n === 'proxy-authorization' || n === 'cookie') return true
  return ['key', 'token', 'secret', 'password'].some((part) => n.includes(part))
}

/** A credential header whose value is typed in rather than a template. */
export const isPlaintextCredential = (name: string, value: unknown) =>
  typeof value === 'string' && value !== '' && !value.includes('{{') && isCredentialHeader(name)

/** An absolute http or https URL with a host (server.go CheckURL). */
export function isHttpUrl(raw: string): boolean {
  try {
    const u = new URL(raw)
    return (u.protocol === 'http:' || u.protocol === 'https:') && u.host !== ''
  } catch {
    return false
  }
}

/** An mcp tool's server and remote tool (ParseServer, parseMCP, mcpToolIssues). */
function mcpIssues(tool: AgentTool, name: string, i: number): AgentIssue[] {
  const out: AgentIssue[] = []
  const err = (message: string, field: string) => out.push({ severity: 'error', message, toolIndex: i, field })
  const server = tool.server && typeof tool.server === 'object' && !Array.isArray(tool.server) ? tool.server : {}
  const url = typeof server.url === 'string' ? server.url.trim() : ''
  if (url === '') err(`MCP tool "${name}" needs the server's url (server.url).`, 'server.url')
  else if (!url.includes('{{') && !isHttpUrl(url)) err(`MCP tool "${name}" needs an http or https server url.`, 'server.url')
  if (typeof tool.tool !== 'string' || tool.tool.trim() === '') {
    err(`MCP tool "${name}" needs the name of the remote tool it calls (tool).`, 'tool')
  }
  const headers: Record<string, unknown> =
    server.headers && typeof server.headers === 'object' && !Array.isArray(server.headers) ? server.headers : {}
  for (const [header, value] of Object.entries(headers)) {
    const canonical = validHeaderName(header.trim()) ? canonicalHeader(header) : header
    if (!validHeaderName(header.trim())) err(`Header name "${header}" is not valid.`, 'server.headers')
    else if (isReservedHeader(header)) {
      err(`Header "${canonical}" is set by the MCP transport and cannot be configured.`, 'server.headers')
    } else if (typeof value !== 'string') err(`Header "${canonical}" must be text.`, 'server.headers')
    else if (!validHeaderValue(value)) err(`Header "${canonical}" has a value with control characters.`, 'server.headers')
  }
  for (const [header, value] of Object.entries(headers)) {
    if (!isPlaintextCredential(header, value)) continue
    out.push({
      severity: 'warning',
      message: `MCP tool "${name}" stores header "${header}" in the workflow. Use ${SECRET_HEADER_HINT} instead.`,
      toolIndex: i,
      field: 'server.headers',
    })
  }
  return out
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
  } else if (kind === MCP_KIND) {
    out.push(...mcpIssues(tool, name, i))
  } else if (!isReadKind(kind)) {
    err(`Tool "${name}" needs a kind: a lookup, an API call, a vector search, a sink or an MCP tool.`, 'kind')
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
