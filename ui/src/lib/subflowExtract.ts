/**
 * "Turn selection into sub-flow": cuts the selected steps out of a workflow
 * into a new sub-flow, and puts one Run sub-flow step in their place, wired to
 * what the steps were wired to.
 *
 * - Edges into the selection go into the call step, and the sub-flow's input
 *   leads to the steps they reached.
 * - Each distinct way out of the selection (a step and the branch it leaves
 *   on) becomes a Return step. With one way out, the call has one plain
 *   output; with more, one named output each.
 * - An error edge out of the selection becomes the call's error edge: the
 *   engine sends a failure the sub-flow does not handle there.
 */
import { CALL_SUBFLOW, SUBFLOW_INPUT, SUBFLOW_RETURN, type SubflowMapping } from './subflow'

/** A step as the editor's canvas holds it. */
export interface CanvasNode {
  id: string
  type?: string
  position: { x: number; y: number }
  data: Record<string, any>
  [key: string]: any
}

/** An edge as the editor's canvas holds it. */
export interface CanvasEdge {
  id: string
  source: string
  target: string
  sourceHandle?: string | null
  targetHandle?: string | null
  data?: Record<string, any>
  [key: string]: any
}

/** A step as the API saves it. */
export interface SavedStep {
  id: string
  type: string
  ref_id?: string
  config: Record<string, any>
  x: number
  y: number
}

/** An edge as the API saves it. */
export interface SavedEdge {
  id: string
  source_id: string
  target_id: string
  source_handle?: string | null
  target_handle?: string | null
  config: Record<string, any>
}

export interface ExtractOptions {
  /** The id of the call step that replaces the selection. */
  callId: string
  /** The id the new sub-flow is saved under. */
  subflowId: string
  name: string
  /** Fields of the caller the sub-flow needs; each becomes a required input of the same name. */
  inputs: string[]
  /** Fields of the sub-flow's result copied back onto the caller's record, each to the same name. */
  outputs: string[]
}

export interface Extracted {
  /** The new sub-flow's steps and edges, ready to save. */
  subflow: { nodes: SavedStep[]; edges: SavedEdge[] }
  /** The caller's canvas with the call step in place of the selection. */
  nodes: CanvasNode[]
  edges: CanvasEdge[]
}

const ERROR = 'error'
const GAP = 240

/** The branch an edge leaves on, the way the engine reads it. */
export function edgeBranch(e: CanvasEdge): string {
  const label = e.data?.label
  if (typeof label === 'string' && label !== '') return label
  return e.sourceHandle ?? ''
}

const notAllowed: Record<string, string> = {
  source: 'A sub-flow has no source: leave the source steps out of the selection.',
  [SUBFLOW_INPUT]: 'The selection holds a Sub-flow input step; this is a sub-flow already.',
  [SUBFLOW_RETURN]: 'The selection holds a Return step; this is a sub-flow already.',
}

function unique(name: string, taken: Set<string>): string {
  let out = name
  for (let i = 2; taken.has(out); i++) out = `${name} ${i}`
  taken.add(out)
  return out
}

function saved(n: CanvasNode, dx: number, dy: number): SavedStep {
  return { id: n.id, type: n.type ?? '', ref_id: n.data?.ref_id, config: { ...n.data }, x: n.position.x + dx, y: n.position.y + dy }
}

function savedEdge(id: string, source: string, target: string, branch: string): SavedEdge {
  return {
    id, source_id: source, target_id: target,
    source_handle: branch || null,
    config: branch ? { label: branch } : {},
  }
}

export function extractSubflow(
  nodes: CanvasNode[], edges: CanvasEdge[], selectedIds: string[], opts: ExtractOptions,
): Extracted | { error: string } {
  const picked = new Set(selectedIds)
  const inside = nodes.filter((n) => picked.has(n.id))
  if (inside.filter((n) => n.type !== 'note').length === 0) {
    return { error: 'Select the steps to move into the sub-flow first.' }
  }
  for (const n of inside) {
    const why = notAllowed[n.type ?? '']
    if (why) return { error: why }
  }

  const internal = edges.filter((e) => picked.has(e.source) && picked.has(e.target))
  const incoming = edges.filter((e) => !picked.has(e.source) && picked.has(e.target))
  const outgoing = edges.filter((e) => picked.has(e.source) && !picked.has(e.target))
  const errorsOut = outgoing.filter((e) => edgeBranch(e) === ERROR)
  const leaving = outgoing.filter((e) => edgeBranch(e) !== ERROR)

  // Each distinct step-and-branch that leaves the selection is a way out. With
  // none, every step that leads nowhere ends the sub-flow.
  const exits: { source: string; branch: string }[] = []
  const exitKey = (source: string, branch: string) => `${source}\u0000${branch}`
  const seen = new Set<string>()
  for (const e of leaving) {
    const k = exitKey(e.source, edgeBranch(e))
    if (!seen.has(k)) { seen.add(k); exits.push({ source: e.source, branch: edgeBranch(e) }) }
  }
  if (exits.length === 0) {
    const leads = new Set(edges.filter((e) => picked.has(e.source) && edgeBranch(e) !== ERROR).map((e) => e.source))
    for (const n of inside) if (n.type !== 'note' && !leads.has(n.id)) exits.push({ source: n.id, branch: '' })
  }

  // One way out is the call's plain output; more each get a name: the branch
  // they leave on, else the step they leave from.
  const taken = new Set<string>()
  const label = (id: string) => String(nodes.find((n) => n.id === id)?.data?.label || id)
  const names = exits.length <= 1
    ? exits.map(() => '')
    : exits.map((x) => unique(x.branch || label(x.source), taken))

  // Lay the sub-flow out where the steps were, between its input and returns.
  const xs = inside.map((n) => n.position.x)
  const ys = inside.map((n) => n.position.y)
  const minX = Math.min(...xs), maxX = Math.max(...xs)
  const minY = Math.min(...ys), maxY = Math.max(...ys)
  const dx = GAP - minX, dy = -minY
  const midY = (maxY - minY) / 2

  const input: SavedStep = {
    id: 'input', type: SUBFLOW_INPUT, x: 0, y: midY,
    config: { label: 'Input', fields: opts.inputs.map((name) => ({ name, required: true })) },
  }
  const returnIds = exits.map((_, i) => (exits.length === 1 ? 'return' : `return_${i + 1}`))
  const returns: SavedStep[] = exits.map((_, i) => ({
    id: returnIds[i], type: SUBFLOW_RETURN, x: maxX + dx + GAP, y: exits.length === 1 ? midY : i * 120,
    config: { label: names[i] ? `Return ${names[i]}` : 'Return', name: names[i] },
  }))

  const entries = [...new Set(incoming.map((e) => e.target))]
  if (entries.length === 0) {
    const reached = new Set(internal.map((e) => e.target))
    for (const n of inside) if (n.type !== 'note' && !reached.has(n.id)) entries.push(n.id)
  }

  const subflow = {
    nodes: [input, ...inside.map((n) => saved(n, dx, dy)), ...returns],
    edges: [
      ...entries.map((t, i) => savedEdge(`in_${i + 1}`, 'input', t, '')),
      ...internal.map((e) => ({
        id: e.id, source_id: e.source, target_id: e.target,
        source_handle: e.sourceHandle ?? null, target_handle: e.targetHandle ?? null, config: { ...(e.data ?? {}) },
      })),
      ...exits.map((x, i) => savedEdge(`out_${i + 1}`, x.source, returnIds[i], x.branch)),
    ],
  }

  // The caller: the call step where the selection was, wired the same way.
  const mapping = (names: string[]): SubflowMapping[] => names.map((n) => ({ from: n, to: n }))
  const call: CanvasNode = {
    id: opts.callId,
    type: CALL_SUBFLOW,
    position: { x: (minX + maxX) / 2, y: (minY + maxY) / 2 },
    data: {
      label: opts.name, subflow_id: opts.subflowId, subflow_name: opts.name, version: 1,
      returns: names, inputs: mapping(opts.inputs), outputs: mapping(opts.outputs),
    },
  }
  const outName = (e: CanvasEdge) => names[exits.findIndex((x) => x.source === e.source && x.branch === edgeBranch(e))] ?? ''
  const rewired: CanvasEdge[] = []
  const wired = new Set<string>()
  const add = (e: CanvasEdge, key: string) => { if (!wired.has(key)) { wired.add(key); rewired.push(e) } }
  for (const e of edges) {
    if (incoming.includes(e)) {
      add({ ...e, target: opts.callId, targetHandle: null }, `in\u0000${e.source}\u0000${edgeBranch(e)}`)
    } else if (errorsOut.includes(e)) {
      add({ ...e, source: opts.callId, sourceHandle: ERROR, data: { ...(e.data ?? {}), label: ERROR } }, `err\u0000${e.target}`)
    } else if (leaving.includes(e)) {
      const name = outName(e)
      add(
        { ...e, source: opts.callId, sourceHandle: name || null, data: { ...(e.data ?? {}), label: name } },
        `out\u0000${name}\u0000${e.target}`,
      )
    }
  }

  return {
    subflow,
    nodes: [...nodes.filter((n) => !picked.has(n.id)), call],
    edges: [...edges.filter((e) => !picked.has(e.source) && !picked.has(e.target)), ...rewired],
  }
}

/**
 * The fields of the caller the selected steps mention anywhere in their
 * settings: a first guess at the inputs the sub-flow needs, for the operator
 * to correct.
 */
export function guessInputs(steps: CanvasNode[], fields: string[]): string[] {
  const text = steps.map((n) => JSON.stringify(n.data ?? {})).join('\n')
  const mentioned = (f: string) => new RegExp(`(^|[^\\w.])${f.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}(?![\\w])`).test(text)
  return fields.filter(mentioned)
}
