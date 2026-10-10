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
}

const base = (workflowId: string) => `/api/workflows/${encodeURIComponent(workflowId)}`

export const proposalsKey = (workflowId: string) => ['proposals', workflowId] as const

export async function listProposals(workflowId: string, signal?: AbortSignal): Promise<Proposal[]> {
  const body = await requestJson<{ data: Proposal[] | null }>(`${base(workflowId)}/proposals`, { signal })
  return body?.data ?? []
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

export function useProposals(workflowId: string) {
  return useQuery({
    queryKey: proposalsKey(workflowId),
    queryFn: ({ signal }) => listProposals(workflowId, signal),
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
