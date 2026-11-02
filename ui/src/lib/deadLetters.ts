// Types and helpers for the dead letter page: records a workflow parked after
// its retries ran out, as Hermod keeps them (see /api/dead-letters). A record
// that fails again while it is waiting is counted on its row, not kept twice.

export type DeadLetterStatus = 'queued' | 'replayed' | 'discarded' | 'delivered' | 'merged'

export interface DeadLetterEvent {
  at: string
  action: string
  by?: string
  detail?: string
  // Identical failures in a row are one event: count says how many, until
  // when the last one was.
  count?: number
  until?: string
}

export interface DeadLetter {
  id: string
  vhost: string
  workflow_id: string
  workflow_name?: string
  message_id: string
  node_id?: string
  sink_id?: string
  error: string
  // failed_at is the latest attempt; first_failed_at the first. failure_count
  // is how many failed in between, missing on a row from before counting.
  failed_at: string
  first_failed_at?: string
  failure_count?: number
  record_key?: string
  // Go sends []byte as base64; decodePayload turns it back into text.
  payload?: string
  payload_size: number
  truncated?: boolean
  metadata?: Record<string, string>
  origin?: { operation?: string; table?: string; schema?: string; before?: string }
  status: DeadLetterStatus
  settled_at?: string
  settled_by?: string
  history?: DeadLetterEvent[]
}

export interface DeadLetterStats {
  // queued counts records; queued_attempts the failed attempts they stand for.
  queued: number
  queued_attempts?: number
  last_24h: number
  top_error?: string
  top_error_count: number
  replayed_this_week: number
}

export interface ReplayStep {
  node_id: string
  node_type?: string
  payload?: Record<string, unknown>
  error?: string
  filtered?: boolean
  skipped?: boolean
}

export interface ReplayOutcome {
  id: string
  status: 'replayed' | 'failed' | 'not_replayed' | 'busy' | 'not_found' | 'already_settled' | 'discarded'
  error?: string
}

export type When = '24h' | '7d' | 'any'

export interface DeadLetterFilters {
  search: string
  workflowId: string | null
  where: string | null
  when: When
  status: DeadLetterStatus | 'all'
  vhost?: string
}

export const STATUS_LABEL: Record<DeadLetterStatus, string> = {
  queued: 'Waiting',
  replayed: 'Replayed',
  discarded: 'Discarded',
  delivered: 'Delivered later',
  merged: 'Merged',
}

// attempts is how many failed attempts a row stands for, at least one.
export function attempts(d: Pick<DeadLetter, 'failure_count'>): number {
  return Math.max(d.failure_count ?? 1, 1)
}

export function attemptsLabel(d: Pick<DeadLetter, 'failure_count'>): string {
  return `×${attempts(d)}`
}

// spanLabel says how long a record kept failing, from its first attempt to
// its latest; nothing for one attempt.
export function spanLabel(first: string | undefined, last: string | undefined): string {
  if (!first || !last) return ''
  const ms = new Date(last).getTime() - new Date(first).getTime()
  if (!Number.isFinite(ms) || ms < 1000) return ''
  const s = Math.round(ms / 1000)
  const h = Math.floor(s / 3600)
  const m = Math.floor((s % 3600) / 60)
  if (h > 0) return `over ${h} h${m ? ` ${m} min` : ''}`
  if (m > 0) return `over ${m} min`
  return `over ${s} s`
}

export function whenLabel(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'medium' })
}

// historyLine is one event of a dead letter's history as the page shows it.
export function historyLine(ev: DeadLetterEvent): string {
  let line = ev.action.replace(/_/g, ' ')
  if (ev.count && ev.count > 1) line += ` ×${ev.count}`
  if (ev.until && ev.until !== ev.at) line += ` until ${whenLabel(ev.until)}`
  if (ev.by) line += ` by ${ev.by}`
  if (ev.detail) line += ` · ${ev.detail}`
  return line
}

// CheckResult is what /api/dead-letters/{id}/check says about the sink a
// record failed at.
export interface CheckResult {
  checked: boolean
  sink_id?: string
  sink_name?: string
  reachable: boolean
  error?: string
}

export function sinceFor(when: When, now: Date = new Date()): string | undefined {
  const hours = when === '24h' ? 24 : when === '7d' ? 24 * 7 : 0
  return hours ? new Date(now.getTime() - hours * 3_600_000).toISOString() : undefined
}

// deadLetterQuery is the list's query string for page (0-based) of size limit.
export function deadLetterQuery(f: DeadLetterFilters, page: number, limit: number, now: Date = new Date()): URLSearchParams {
  const q = new URLSearchParams()
  if (f.search.trim()) q.set('search', f.search.trim())
  if (f.workflowId) q.set('workflow_id', f.workflowId)
  if (f.where) q.set('where', f.where)
  if (f.status !== 'all') q.set('status', f.status)
  const since = sinceFor(f.when, now)
  if (since) q.set('since', since)
  if (f.vhost && f.vhost !== 'all') q.set('vhost', f.vhost)
  q.set('limit', String(limit))
  q.set('offset', String(page * limit))
  return q
}

export function decodePayload(b64: string | undefined): string {
  if (!b64) return ''
  try {
    const bytes = Uint8Array.from(atob(b64), (c) => c.charCodeAt(0))
    return new TextDecoder().decode(bytes)
  } catch {
    return b64
  }
}

// prettyJSON indents JSON text, and leaves anything else as it is.
export function prettyJSON(text: string): string {
  try {
    return JSON.stringify(JSON.parse(text), null, 2)
  } catch {
    return text
  }
}

export function failedAtLabel(d: Pick<DeadLetter, 'node_id' | 'sink_id'>, nodeNames: Record<string, string>): string {
  if (d.node_id) return `Node: ${nodeNames[d.node_id] || d.node_id}`
  if (d.sink_id) return `Sink: ${nodeNames[d.sink_id] || d.sink_id}`
  return 'Unknown'
}

export interface FieldChange {
  path: string
  before: unknown
  after: unknown
}

export interface EditCheck {
  valid: boolean
  changes: FieldChange[]
}

// checkEdit says whether edited parses as JSON and which fields differ from
// original, by path, sorted.
export function checkEdit(original: string, edited: string): EditCheck {
  let after: unknown
  try {
    after = JSON.parse(edited)
  } catch {
    return { valid: false, changes: [] }
  }
  let before: unknown
  try {
    before = JSON.parse(original)
  } catch {
    before = undefined
  }
  const changes: FieldChange[] = []
  diff(before, after, '', changes)
  changes.sort((a, b) => a.path.localeCompare(b.path))
  return { valid: true, changes }
}

function isObject(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

function diff(a: unknown, b: unknown, path: string, out: FieldChange[]) {
  if (isObject(a) && isObject(b)) {
    for (const k of new Set([...Object.keys(a), ...Object.keys(b)])) {
      diff(a[k], b[k], path ? `${path}.${k}` : k, out)
    }
    return
  }
  if (Array.isArray(a) && Array.isArray(b)) {
    for (let i = 0; i < Math.max(a.length, b.length); i++) {
      diff(a[i], b[i], `${path}[${i}]`, out)
    }
    return
  }
  if (JSON.stringify(a) !== JSON.stringify(b)) {
    out.push({ path: path || '(whole message)', before: a, after: b })
  }
}

export function describeValue(v: unknown): string {
  return v === undefined ? '(none)' : JSON.stringify(v)
}
