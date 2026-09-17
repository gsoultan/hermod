/**
 * Reading, reviewing and rewriting a workflow export bundle before it is imported.
 *
 * Everything here is pure, because the hard part of an import is arithmetic on
 * references rather than anything visual. A workflow names a source in four
 * different places (see `SOURCE_REF_SITES` below), and giving a resource a new
 * ID means finding all of them — miss one and the imported workflow starts and
 * then fails on a reference to something that no longer exists. That is exactly
 * the bug the export side had, so the rule is written down once, here and in
 * `collectWorkflowRefs` on the server, and tested.
 */

export type ConflictDecision = 'overwrite' | 'copy'
export type ResourceKind = 'workflow' | 'source' | 'sink'

export interface ImportNode {
  id: string
  type: string
  ref_id?: string
  config?: Record<string, any> | null
  [key: string]: any
}

export interface ImportWorkflow {
  id: string
  name: string
  vhost?: string
  dead_letter_sink_id?: string
  nodes: ImportNode[]
  edges: any[]
  [key: string]: any
}

export interface ImportResource {
  id: string
  name: string
  type: string
  vhost?: string
  config: Record<string, any>
  [key: string]: any
}

export interface MissingRef {
  kind: string
  id: string
  node_id?: string
}

export interface ImportBundle {
  workflow: ImportWorkflow
  sources: ImportResource[]
  sinks: ImportResource[]
  missing_refs?: MissingRef[]
}

/**
 * The config keys through which a *node* names a source without being a source
 * node. `db_lookup` writes `sourceId`; the enrichment SQL node reads and writes
 * both spellings. Kept in step with `nodeConfigSourceKeys` in
 * internal/workflow/transport/http/workflow.go.
 */
export const NODE_CONFIG_SOURCE_KEYS = ['sourceId', 'sourceID'] as const

/** Human-readable list of every place a source ID can appear, for the docs above. */
export const SOURCE_REF_SITES = [
  'a node typed "source", in ref_id',
  'any node config, under sourceId or sourceID',
  'a batch_sql source config, under source_id',
] as const

/**
 * Decides whether a config key holds a credential, by shape rather than by a
 * list of names — the same rule `internal/storage/configsecrets` applies on the
 * server, and for the same reason: a connector added next year with an
 * `smtp_password` is covered on the day it is written.
 */
const CREDENTIAL_SUFFIXES = [
  'password', 'passwd', 'secret', 'token', 'key', 'credentials', 'dsn', 'apikey',
]

/** Names that match the rule above but are not credentials. Each needs a reason. */
const NOT_CREDENTIALS = new Set([
  's3_key',        // object key *prefix* — a path
  'key_prefix',    // object key prefix
  'keyspace',      // Cassandra keyspace name
  'keyfield',      // a path into the message, not a secret
  'keycolumn',     // a column name
  'primarykey',    // a column name
  'partitionkey',  // a routing field name
  'keyformat',     // how to read the key, not the key
  'sortkey',       // a column name
  'idempotencykey', // a field name
])

export function isCredentialKey(key: string): boolean {
  const k = key.toLowerCase().replace(/[-\s]/g, '_')
  if (NOT_CREDENTIALS.has(k) || NOT_CREDENTIALS.has(k.replace(/_/g, ''))) return false
  return CREDENTIAL_SUFFIXES.some((s) => k === s || k.endsWith(`_${s}`) || k.endsWith(s))
}

/** Config keys that address something outside this instance. */
const ENDPOINT_KEYS = new Set([
  'url', 'endpoint', 'base_url', 'baseurl', 'host', 'uri', 'webhook_url', 'api_url', 'server',
])

function isEndpointKey(key: string): boolean {
  return ENDPOINT_KEYS.has(key.toLowerCase().replace(/[-\s]/g, '_'))
}

/**
 * The subtype a stored node claims.
 *
 * The editor serialises a node's whole React Flow `data` object into `config`,
 * and the readers in this codebase disagree about which key holds the subtype:
 * server-side validation reads `transType`, internal/ai reads `type`, and nodes
 * that are not transformations carry it as the node type itself. Accept all
 * three rather than betting on one.
 */
export function nodeTransType(node: ImportNode): string {
  return (
    (typeof node.config?.transType === 'string' ? node.config.transType : '') ||
    (typeof node.config?.type === 'string' ? node.config.type : '') ||
    node.type ||
    ''
  )
}

export type NodeReviewReason = 'source-reference' | 'credential' | 'endpoint'

export interface ReviewNode {
  node: ImportNode
  transType: string
  reasons: NodeReviewReason[]
  /** The config keys that caused each reason, so the UI can point at them. */
  fields: string[]
}

/**
 * The nodes worth showing an operator before this bundle runs somewhere else:
 * the ones holding a reference to a source, a credential, or an address outside
 * this instance. A mapping or filter node describes a shape and travels
 * unchanged, so it is not in the way.
 *
 * Source and sink nodes are deliberately excluded — their configuration lives
 * on the source/sink record, which gets its own step.
 */
export function collectReviewNodes(wf: Pick<ImportWorkflow, 'nodes'>): ReviewNode[] {
  const out: ReviewNode[] = []

  for (const node of wf.nodes ?? []) {
    if (node.type === 'source' || node.type === 'sink') continue
    const config = node.config
    if (!config || typeof config !== 'object') continue

    const reasons = new Set<NodeReviewReason>()
    const fields: string[] = []

    for (const [key, value] of Object.entries(config)) {
      // An empty value is nothing to review: the node is already asking to be
      // configured, and flagging it here would only add noise.
      if (value === '' || value === null || value === undefined) continue

      if ((NODE_CONFIG_SOURCE_KEYS as readonly string[]).includes(key)) {
        reasons.add('source-reference')
        fields.push(key)
      } else if (isCredentialKey(key)) {
        reasons.add('credential')
        fields.push(key)
      } else if (isEndpointKey(key) && typeof value === 'string') {
        reasons.add('endpoint')
        fields.push(key)
      }
    }

    if (reasons.size > 0) {
      out.push({ node, transType: nodeTransType(node), reasons: [...reasons], fields })
    }
  }

  return out
}

export function parseImportBundle(json: string): { bundle: ImportBundle } | { error: string } {
  let data: any
  try {
    data = JSON.parse(json)
  } catch {
    return { error: 'That is not valid JSON. Paste the file an export produced, or upload it.' }
  }
  if (!data || typeof data !== 'object' || Array.isArray(data)) {
    return { error: 'A bundle is a JSON object. This is not one.' }
  }

  // Two shapes: a bundle, and the bare workflow older exports wrote.
  const workflow = data.workflow && typeof data.workflow === 'object' ? data.workflow : data
  if (!workflow.id || !workflow.name) {
    return { error: 'This JSON is neither a workflow nor an export bundle: it has no workflow id and name.' }
  }

  return {
    bundle: {
      workflow: { ...workflow, nodes: workflow.nodes ?? [], edges: workflow.edges ?? [] },
      sources: Array.isArray(data.sources) ? data.sources : [],
      sinks: Array.isArray(data.sinks) ? data.sinks : [],
      missing_refs: Array.isArray(data.missing_refs) ? data.missing_refs : undefined,
    },
  }
}

export interface Conflict {
  kind: ResourceKind
  id: string
  incomingName: string
  existingName: string
}

type Existing = {
  workflows: { id: string; name: string }[]
  sources: { id: string; name: string }[]
  sinks: { id: string; name: string }[]
}

/**
 * Which of the bundle's IDs already name something here. Importing one of these
 * without a decision overwrites a record that is potentially in use — which is
 * the part of import that used to happen silently.
 */
export function detectConflicts(bundle: ImportBundle, existing: Existing): Conflict[] {
  const out: Conflict[] = []
  const index = (rows: { id: string; name: string }[]) => new Map(rows.map((r) => [r.id, r.name]))

  const wfs = index(existing.workflows ?? [])
  if (wfs.has(bundle.workflow.id)) {
    out.push({
      kind: 'workflow',
      id: bundle.workflow.id,
      incomingName: bundle.workflow.name,
      existingName: wfs.get(bundle.workflow.id)!,
    })
  }

  const srcs = index(existing.sources ?? [])
  for (const s of bundle.sources) {
    if (srcs.has(s.id)) {
      out.push({ kind: 'source', id: s.id, incomingName: s.name, existingName: srcs.get(s.id)! })
    }
  }

  const snks = index(existing.sinks ?? [])
  for (const s of bundle.sinks) {
    if (snks.has(s.id)) {
      out.push({ kind: 'sink', id: s.id, incomingName: s.name, existingName: snks.get(s.id)! })
    }
  }

  return out
}

/**
 * Applies the operator's per-resource choice, returning a new bundle.
 *
 * `copy` means "leave what is here alone": the resource gets a fresh ID, and
 * every reference to the old one inside this bundle is repointed at it. A
 * reference to a resource that was *not* copied keeps its ID, which is how a
 * workflow can reuse an existing connection while replacing another.
 */
function randomID(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID()
  }
  // Not every browser exposes randomUUID, and a crash at submit is a poor way
  // to discover that. Uniqueness within one import is all this needs.
  return `imported-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`
}

export function applyConflictDecisions(
  bundle: ImportBundle,
  decisions: Record<string, ConflictDecision>,
  newId: () => string = randomID,
): ImportBundle {
  const next: ImportBundle = JSON.parse(JSON.stringify(bundle))

  // old ID -> new ID, for the resources being copied.
  const remap = new Map<string, string>()
  for (const s of [...next.sources, ...next.sinks]) {
    if (decisions[s.id] === 'copy') remap.set(s.id, newId())
  }

  const rewrite = (id: string | undefined): string | undefined =>
    id && remap.has(id) ? remap.get(id)! : id

  for (const s of next.sources) {
    // A batch_sql source delegates its connection to another source. This is
    // read before s.id is rewritten, so a source that delegates to itself —
    // which should not happen, but is cheap to survive — still resolves.
    if (s.config && typeof s.config.source_id === 'string') {
      s.config.source_id = rewrite(s.config.source_id)!
    }
  }
  for (const s of next.sources) s.id = rewrite(s.id)!
  for (const s of next.sinks) s.id = rewrite(s.id)!

  for (const node of next.workflow.nodes) {
    if (node.ref_id) node.ref_id = rewrite(node.ref_id)!
    if (node.config && typeof node.config === 'object') {
      for (const key of NODE_CONFIG_SOURCE_KEYS) {
        if (typeof node.config[key] === 'string') {
          node.config[key] = rewrite(node.config[key])!
        }
      }
    }
  }

  if (next.workflow.dead_letter_sink_id) {
    next.workflow.dead_letter_sink_id = rewrite(next.workflow.dead_letter_sink_id)!
  }

  // The workflow last: its own ID is not referenced by anything in the bundle.
  if (decisions[next.workflow.id] === 'copy') {
    next.workflow.id = newId()
  }

  return next
}

/** What the import will do, for the review step. */
export interface ImportPlanRow {
  kind: ResourceKind
  id: string
  name: string
  action: 'create' | 'update'
}

export function buildImportPlan(bundle: ImportBundle, existing: Existing): ImportPlanRow[] {
  const has = (rows: { id: string }[], id: string) => rows.some((r) => r.id === id)
  const rows: ImportPlanRow[] = []

  for (const s of bundle.sources) {
    rows.push({ kind: 'source', id: s.id, name: s.name, action: has(existing.sources ?? [], s.id) ? 'update' : 'create' })
  }
  for (const s of bundle.sinks) {
    rows.push({ kind: 'sink', id: s.id, name: s.name, action: has(existing.sinks ?? [], s.id) ? 'update' : 'create' })
  }
  rows.push({
    kind: 'workflow',
    id: bundle.workflow.id,
    name: bundle.workflow.name,
    action: has(existing.workflows ?? [], bundle.workflow.id) ? 'update' : 'create',
  })

  return rows
}

export interface NameCollision {
  kind: ResourceKind
  id: string
  name: string
}

/**
 * Bundle resources whose name is already held by a *different* record here.
 *
 * `sources.name` and `sinks.name` are `NOT NULL UNIQUE`
 * (internal/storage/sql/queries.go), so this is not a style preference: the
 * insert fails with whatever the driver says about constraint 2067, which is
 * not a sentence anyone should have to read. Catching it before the request
 * means the wizard can point at the field instead.
 *
 * The same id holding the same name is an update, not a collision.
 */
export function nameCollisions(bundle: ImportBundle, existing: Existing): NameCollision[] {
  const out: NameCollision[] = []
  // Trimmed, but not case-folded: SQLite's UNIQUE is case-sensitive by default,
  // so treating "main" and "Main" as the same would refuse a legal name.
  const norm = (n: string) => (n ?? '').trim()

  const check = (kind: 'source' | 'sink', rows: ImportResource[], theirs: { id: string; name: string }[]) => {
    const taken = new Map<string, string>()
    for (const r of theirs ?? []) taken.set(norm(r.name), r.id)
    for (const r of rows) {
      const holder = taken.get(norm(r.name))
      if (holder && holder !== r.id) out.push({ kind, id: r.id, name: r.name })
    }
  }

  check('source', bundle.sources, existing.sources)
  check('sink', bundle.sinks, existing.sinks)
  return out
}

/** Every name of the given kind that is spoken for on this instance. */
export function takenNames(existing: Existing, kind: 'source' | 'sink', exceptID?: string): Set<string> {
  const rows = kind === 'source' ? existing.sources : existing.sinks
  return new Set((rows ?? []).filter((r) => r.id !== exceptID).map((r) => (r.name ?? '').trim()))
}

/**
 * The nearest free name to `base`. Used when a resource is imported as a copy,
 * which by definition cannot keep the name of the thing it sits beside.
 */
export function suggestFreeName(base: string, taken: Set<string>): string {
  const trimmed = (base ?? '').trim()
  if (!taken.has(trimmed)) return trimmed

  // "Main (copy)" asked about again becomes "Main (copy 2)", not
  // "Main (copy) (copy)".
  // The stem is only for numbering. Handing back the bare stem would rename a
  // copy to the name the *original* wants, which reads as the original.
  const stem = trimmed.replace(/\s*\(copy(?:\s+\d+)?\)$/, '')
  if (!taken.has(`${stem} (copy)`)) return `${stem} (copy)`
  for (let n = 2; n < 1000; n++) {
    const candidate = `${stem} (copy ${n})`
    if (!taken.has(candidate)) return candidate
  }
  return `${stem} (copy ${Date.now().toString(36)})`
}
