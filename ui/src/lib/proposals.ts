import { useQuery } from '@tanstack/react-query'
import { requestJson } from '@/lib/executions'

/**
 * Self-healing proposals: internal/selfheal and its transport/http handler.
 * A proposal is a small patch to a workflow that applies only when an editor
 * approves it; applying goes through the workflow update path, so it lands in
 * the version history and can be rolled back.
 */

export type ProposalStatus = 'pending' | 'applied' | 'rejected' | 'stale'

/** selfheal.Change: a JSON Patch "replace" with the value it replaces. */
export interface ProposalChange {
  op: string
  path: string
  before: unknown
  after: unknown
}

/** How a flow ran over a stretch of time: advisor.Summary. */
export interface FlowReadings {
  throughput: number
  step_time_ms: number
  /** Failed step runs and dead letters over messages, 0 to 1. */
  error_rate: number
  messages: number
  window_seconds: number
}

/** advisor.Effect: the flow before an applied fix and since. */
export interface ProposalEffect {
  verdict: 'better' | 'worse' | 'same'
  before: FlowReadings
  after: FlowReadings
}

export interface Proposal {
  id: string
  workflow_id: string
  kind: string
  node_id?: string
  title: string
  reason: string
  patch: ProposalChange[] | null
  status: ProposalStatus
  occurrences: number
  created_at: string
  last_seen_at: string
  decided_at?: string
  decided_by?: string
  /** The version before the fix was applied: what a rollback restores. */
  previous_version?: number
  applied_version?: number
  /** Set on the newest applied fix once the flow has run long enough since. */
  effect?: ProposalEffect
  /** The effect is not measurable yet: too few readings since the fix. */
  effect_pending?: boolean
}

const base = (workflowId: string) => `/api/workflows/${encodeURIComponent(workflowId)}`

export const proposalsKey = (workflowId: string) => ['proposals', workflowId] as const

export async function listProposals(workflowId: string, signal?: AbortSignal): Promise<Proposal[]> {
  const body = await requestJson<{ data: Proposal[] | null }>(`${base(workflowId)}/proposals`, { signal })
  return body?.data ?? []
}

/**
 * Turns one of the advisor's current recommendations into a pending proposal.
 * The request names the finding only; the server decides what the fix changes.
 */
export function proposeFix(workflowId: string, finding: { rule: string; node_id?: string }): Promise<Proposal> {
  return requestJson<Proposal>(`${base(workflowId)}/proposals`, {
    method: 'POST',
    body: JSON.stringify({ rule: finding.rule, node_id: finding.node_id ?? '' }),
  })
}

export function approveProposal(workflowId: string, proposalId: string): Promise<Proposal> {
  return requestJson<Proposal>(`${base(workflowId)}/proposals/${encodeURIComponent(proposalId)}/approve`, { method: 'POST' })
}

export function rejectProposal(workflowId: string, proposalId: string): Promise<Proposal> {
  return requestJson<Proposal>(`${base(workflowId)}/proposals/${encodeURIComponent(proposalId)}/reject`, { method: 'POST' })
}

/** The existing workflow rollback: POST /api/workflows/{id}/rollback/{version}. */
export function rollbackWorkflow(workflowId: string, version: number): Promise<unknown> {
  return requestJson<unknown>(`${base(workflowId)}/rollback/${version}`, { method: 'POST' })
}

/** The advisor reads a running flow every 30 seconds, so an effect cannot appear sooner. */
const EFFECT_REFRESH_MS = 30_000

export function useProposals(workflowId: string) {
  return useQuery({
    queryKey: proposalsKey(workflowId),
    queryFn: ({ signal }) => listProposals(workflowId, signal),
    // A fresh fix has no comparison yet; ask again until the server has one.
    refetchInterval: (query) => (query.state.data?.some((p) => p.effect_pending) ? EFFECT_REFRESH_MS : false),
  })
}

/** A patch value as it appears in JSON, so "100ms" and 100 read differently. */
export function formatPatchValue(value: unknown): string {
  if (value === undefined) return '—'
  try {
    return JSON.stringify(value)
  } catch {
    return String(value)
  }
}
