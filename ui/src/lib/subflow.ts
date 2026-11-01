/**
 * Sub-flows: a workflow that starts at a Sub-flow input step instead of a
 * source, and that another workflow runs through a Run sub-flow step.
 *
 * The engine splices the pinned version of the sub-flow into the caller when
 * the caller starts (internal/engine/registry/subflow). A step inside a
 * sub-flow then shows up in a run under the call step's id, joined with `~`.
 */

export const SUBFLOW_INPUT = 'subflow_input'
export const SUBFLOW_RETURN = 'subflow_return'
export const CALL_SUBFLOW = 'call_subflow'

/** Joins a call step's id to the ids of the steps spliced in for it. */
export const SUBFLOW_SEPARATOR = '~'

export interface SubflowField {
  name: string
  required: boolean
}

export interface SubflowMapping {
  from: string
  to: string
}

/** A saved node, as the API returns it in a workflow or a version. */
export interface SavedNode {
  id: string
  type: string
  config?: Record<string, any>
}

/** The inputs a sub-flow declares on its Sub-flow input step. */
export function subflowFields(nodes?: SavedNode[] | null): SubflowField[] {
  const input = (nodes ?? []).find((n) => n.type === SUBFLOW_INPUT)
  const fields: any[] = Array.isArray(input?.config?.fields) ? input!.config!.fields : []
  return fields
    .map((f) => ({ name: String(f?.name ?? '').trim(), required: f?.required === true }))
    .filter((f) => f.name !== '')
}

/** The names of a sub-flow's Return steps, each once, in the order drawn. */
export function subflowReturns(nodes?: SavedNode[] | null): string[] {
  const names: string[] = []
  for (const n of nodes ?? []) {
    if (n.type !== SUBFLOW_RETURN) continue
    const name = String(n.config?.name ?? '').trim()
    if (!names.includes(name)) names.push(name)
  }
  return names
}

export function isSubflow(nodes?: SavedNode[] | null): boolean {
  return !!nodes?.some((n) => n.type === SUBFLOW_INPUT)
}

/** How many sub-flows deep a step in a run sits: 0 for the workflow's own. */
export function callDepth(nodeId: string): number {
  return nodeId.split(SUBFLOW_SEPARATOR).length - 1
}

export function versionLabel(version: unknown): string {
  if (version === 'latest') return 'Latest saved (not pinned)'
  if (typeof version === 'number' && version > 0) return `v${version}`
  return 'Not pinned'
}

/**
 * The steps a sub-flow runs, by name, in the order drawn: what the Run
 * sub-flow step shows of the version it pins. The input, the returns and
 * canvas notes are left out.
 */
export function subflowSteps(nodes?: SavedNode[] | null): { id: string; type: string; label: string }[] {
  return (nodes ?? [])
    .filter((n) => n.type !== SUBFLOW_INPUT && n.type !== SUBFLOW_RETURN && n.type !== 'note')
    .map((n) => ({ id: n.id, type: n.type, label: String(n.config?.label || n.id) }))
}

/** The newest saved version, or 0 when there is none. */
export function newestVersion(versions: { version: number }[]): number {
  return versions.reduce((max, v) => Math.max(max, v.version), 0)
}
