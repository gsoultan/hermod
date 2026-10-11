import { apiFetch } from '@/api'

/** A stored record that points at an asset (internal/lineage Ref). */
export interface LineageRef {
  kind: 'source' | 'sink'
  id: string
  name: string
  type: string
}

/** A data asset or a workflow (internal/lineage Node). */
export interface LineageNode {
  id: string
  kind: string
  label: string
  system?: string
  locator?: string
  /** False for an asset known only by its source or sink record. */
  resolved: boolean
  vhost?: string
  active?: boolean
  refs?: LineageRef[]
}

/** One read or write, pointing the way data flows (internal/lineage Edge). */
export interface LineageEdge {
  from: string
  to: string
  relation: 'reads' | 'looks_up' | 'retrieves' | 'predicts' | 'writes' | 'dead_letter' | 'trains'
  workflow_id?: string
  node_id?: string
}

/** GET /api/vhosts/{vhost}/lineage (internal/lineage/endpoint GraphResponse). */
export interface LineageGraph {
  vhost: string
  nodes: LineageNode[]
  edges: LineageEdge[]
  summary: { workflows: number; assets: number; unresolved: number; edges: number }
}

/** One node downstream of the root (internal/lineage Impacted). */
export interface ImpactedNode {
  node: LineageNode
  depth: number
  path: string[]
  relation: LineageEdge['relation']
}

/** GET /api/vhosts/{vhost}/lineage/impact (internal/lineage/endpoint ImpactResponse). */
export interface LineageImpact {
  vhost: string
  root: LineageNode
  impacted: ImpactedNode[]
  workflows: number
  assets: number
}

export const lineageKey = (vhost: string) => ['lineage', vhost] as const
export const impactKey = (vhost: string, node: string) => ['lineage', vhost, 'impact', node] as const

const base = (vhost: string) => `/api/vhosts/${encodeURIComponent(vhost || 'all')}/lineage`

export async function getLineage(vhost: string, signal?: AbortSignal): Promise<LineageGraph> {
  const res = await apiFetch(base(vhost), { signal, silent: true })
  return (await res.json()) as LineageGraph
}

export async function getImpact(vhost: string, node: string, signal?: AbortSignal): Promise<LineageImpact> {
  const res = await apiFetch(`${base(vhost)}/impact?node=${encodeURIComponent(node)}`, { signal, silent: true })
  return (await res.json()) as LineageImpact
}

/** The HTTP status an apiFetch error carries, if any. */
export const errorStatus = (err: unknown): number | undefined =>
  (err as { status?: number } | null)?.status

const relationWords: Record<LineageEdge['relation'], string> = {
  reads: 'reads',
  looks_up: 'looks up',
  retrieves: 'retrieves from',
  predicts: 'predicts with',
  writes: 'writes',
  dead_letter: 'dead-letters to',
  trains: 'trains',
}

/** The relation in words, as the impact list reads it. */
export const relationLabel = (r: LineageEdge['relation']) => relationWords[r] ?? r
