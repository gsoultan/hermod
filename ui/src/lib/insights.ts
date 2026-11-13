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
