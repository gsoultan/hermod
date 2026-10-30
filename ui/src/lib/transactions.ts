import { requestJson } from '@/lib/executions'

/**
 * The transactions of a workflow and what their runs did, read from
 * GET /api/workflows/{id}/transactions (registry.TransactionRuns).
 *
 * A transaction is a set of sink nodes that commit together, or a Transaction
 * Group node. The server reads the record of runs and the transaction log from
 * the state store, so a stopped workflow answers too.
 */

export type RunOutcome = 'committed' | 'rolled_back' | 'in_doubt' | 'refused'

/** One member's part in a run (txgroup.RunMember). */
export interface RunMember {
  id: string
  rows: number
  /** The member whose write or vote ended the run. */
  failed?: boolean
  /** In a refused run: the member the event produced no row for. */
  missing?: boolean
}

/** One batch the group wrote, or one event it refused (txgroup.Run). */
export interface TransactionRun {
  at: string
  outcome: RunOutcome
  duration_ms: number
  members: RunMember[]
  error?: string
}

/** A member's totals (txgroup.MemberStats). */
export interface MemberStats {
  id: string
  name?: string
  committed: number
  rolled_back: number
  in_doubt: number
  missing: number
  last_error?: string
  last_error_at?: string
}

export interface TransactionStats {
  committed: number
  rolled_back: number
  in_doubt: number
  refused: number
  members: MemberStats[]
  /** Newest first. */
  recent: TransactionRun[]
}

/** What the transaction log holds now (twopc.PendingTx). */
export interface PendingTransaction {
  tx_id: string
  state: string
  started_at: string
  prepared_at?: string
  /** Members not yet resolved. */
  participants: string[] | null
}

export interface TransactionMember {
  id: string
  name: string
  sink_id: string
  sink_type?: string
}

export interface WorkflowTransaction {
  name: string
  kind: 'commit_together' | 'group'
  node_id?: string
  require_every_member: boolean
  members: TransactionMember[]
  stats: TransactionStats
  pending: PendingTransaction[]
  problem?: string
}

export const transactionsKey = (workflowId: string) => ['workflow-transactions', workflowId] as const

export async function listTransactions(workflowId: string, signal?: AbortSignal): Promise<WorkflowTransaction[]> {
  const body = await requestJson<{ transactions?: WorkflowTransaction[] | null }>(
    `/api/workflows/${encodeURIComponent(workflowId)}/transactions`,
    { signal },
  )
  return Array.isArray(body?.transactions) ? body.transactions : []
}

/** How each outcome is named and coloured. */
export const OUTCOMES: Record<RunOutcome, { label: string; color: string }> = {
  committed: { label: 'Committed', color: 'teal' },
  rolled_back: { label: 'Rolled back', color: 'red' },
  in_doubt: { label: 'In doubt', color: 'orange' },
  refused: { label: 'Refused: missing row', color: 'grape' },
}
