import { requestJson } from '@/lib/executions'
import type { LookupPlanFinding, LookupPlanTable } from '@/lib/insights'

/** The answer to a speed check of the statement in the SQL builder. */
export interface SpeedCheck {
  /** The statement as the engine sends it, with $n where message values go. */
  sql: string
  scans: unknown[] | null
  tables: LookupPlanTable[] | null
  findings: LookupPlanFinding[]
  /** The planner's estimate of the work, in its own units. Not a duration. */
  cost: number
  /** The whole statement as written, with each faulted condition rewritten. */
  rewritten?: string
  /** The planner's estimate for the rewritten statement, against cost. */
  rewritten_cost?: number
}

export interface SpeedCheckRequest {
  config: { type: string; config: unknown }
  query: string
}

/**
 * Plans the statement on its database and reads the plan against the
 * tables' indexes. Nothing runs: the server only plans, in a read-only
 * transaction that it rolls back, so a write statement is safe to check.
 */
export function checkSpeed(req: SpeedCheckRequest, signal?: AbortSignal): Promise<SpeedCheck> {
  return requestJson<SpeedCheck>('/api/sources/query/speed-check', { method: 'POST', body: JSON.stringify(req), signal })
}

/**
 * How many times less work the planner expects from the rewrite, rounded
 * down, or null when it does not expect at least twice less.
 */
export function workSaved(c: SpeedCheck): number | null {
  if (!c.rewritten || !c.rewritten_cost || c.rewritten_cost <= 0) return null
  const times = Math.floor(c.cost / c.rewritten_cost)
  return times >= 2 ? times : null
}
