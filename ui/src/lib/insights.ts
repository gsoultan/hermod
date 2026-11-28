import { useQuery } from '@tanstack/react-query'
import { requestJson } from '@/lib/executions'

/** advisor.Report states. */
export type InsightsState = 'not_running' | 'collecting' | 'ready'
export type InsightSeverity = 'high' | 'medium' | 'low'

export interface InsightEvidence {
  label: string
  value: string
}

/** advisor.Finding: one recommendation and the numbers it was drawn from. */
export interface Insight {
  rule: string
  severity: InsightSeverity
  node_id?: string
  sink_id?: string
  title: string
  detail: string
  recommendation: string
  evidence?: InsightEvidence[]
  /** The change Hermod can propose for this finding; absent when only a person can make it. */
  fix?: string
}

/** advisor.Report: the advisor's reading of one flow's recent window. */
export interface InsightsReport {
  workflow_id: string
  state: InsightsState
  window_start?: string
  window_end?: string
  samples: number
  processed: number
  throughput: number
  bottleneck_node_id?: string
  findings: Insight[]
  /** The server has a model configured to explain the findings. */
  can_explain?: boolean
}

/** advisor.Explanation: a model's plain reading of the findings. */
export interface InsightsExplanation {
  text: string
  provider: string
  model: string
}

export const insightsKey = (workflowId: string) => ['insights', workflowId] as const

/** The server reads each running flow every 30 seconds; asking more often returns the same report. */
export const INSIGHTS_REFRESH_MS = 30_000

export function getInsights(workflowId: string, signal?: AbortSignal): Promise<InsightsReport> {
  return requestJson<InsightsReport>(`/api/workflows/${encodeURIComponent(workflowId)}/insights`, { signal })
}

/** Asks the server's model to explain the flow's current findings. It spends the vhost's AI budget. */
export function explainInsights(workflowId: string): Promise<InsightsExplanation> {
  return requestJson<InsightsExplanation>(`/api/workflows/${encodeURIComponent(workflowId)}/insights/explain`, { method: 'POST' })
}

export function useInsights(workflowId: string, enabled: boolean) {
  return useQuery({
    queryKey: insightsKey(workflowId),
    queryFn: ({ signal }) => getInsights(workflowId, signal),
    enabled: enabled && Boolean(workflowId),
    refetchInterval: INSIGHTS_REFRESH_MS,
  })
}

export type LookupPlanSeverity = 'ok' | 'info' | 'warn'

export interface LookupPlanFinding {
  rule: string
  severity: LookupPlanSeverity
  /** The table it is about, as schema.name. */
  table?: string
  title: string
  detail: string
  /** SQL for a person to review and run. Hermod never runs it. */
  proposal?: string
  /** A condition meaning the same as the query's, that an existing index can serve. */
  rewrite?: string
}

export interface LookupPlanIndex {
  name: string
  method: string
  keys: string[]
  unique?: boolean
  definition?: string
}

export interface LookupPlanTable {
  schema: string
  name: string
  /** The catalog's estimate; -1 when the table was never analyzed. */
  rows: number
  indexes: LookupPlanIndex[]
}

export interface LookupPlanNode {
  node_id: string
  label?: string
  source?: string
  /** The statement planned, with $n where message values go. */
  sql?: string
  scans: unknown[] | null
  tables: LookupPlanTable[] | null
  findings: LookupPlanFinding[] | null
  /** Why the node could not be checked. */
  error?: string
}

/**
 * Plans each DB Lookup step's query on its source and reads it against the
 * tables' indexes. The server only plans, in a read-only transaction: nothing
 * runs and nothing changes. It connects to the flow's sources, so it takes an editor.
 */
export function checkLookupPlans(workflowId: string): Promise<{ nodes: LookupPlanNode[] }> {
  return requestJson<{ nodes: LookupPlanNode[] }>(`/api/workflows/${encodeURIComponent(workflowId)}/insights/lookup-plans`, { method: 'POST' })
}

/** advisor.Card: something Hermod did about a database, or that needs a person. */
export interface DatabaseCard {
  rule: string
  title: string
  detail: string
  /** The step of this flow the card is about. */
  node_id?: string
  evidence?: InsightEvidence[]
}

/** advisor.DatabaseView: one database a flow reaches. */
export interface DatabaseView {
  source_id: string
  source: string
  /** Connections Hermod found it gets; 0 when it found no limit. */
  learned: number
  /** Long queries Hermod runs here at once; 0 for no bound. */
  limit: number
  /** Hermod's queries here now, from every flow, and the long ones among them. */
  running: number
  long: number
  fixed: DatabaseCard[]
  needs_you: DatabaseCard[]
}

export const databasesKey = (workflowId: string) => ['insights', workflowId, 'databases'] as const

/** How often the databases read refreshes: it is memory only, so it is cheap. */
export const DATABASES_REFRESH_MS = 10_000

export function getDatabases(workflowId: string, signal?: AbortSignal): Promise<{ databases: DatabaseView[] }> {
  return requestJson<{ databases: DatabaseView[] }>(`/api/workflows/${encodeURIComponent(workflowId)}/insights/databases`, { signal })
}

export function useDatabases(workflowId: string, enabled: boolean) {
  return useQuery({
    queryKey: databasesKey(workflowId),
    queryFn: ({ signal }) => getDatabases(workflowId, signal),
    enabled: enabled && Boolean(workflowId),
    refetchInterval: DATABASES_REFRESH_MS,
  })
}
