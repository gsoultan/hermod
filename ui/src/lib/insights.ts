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
}

export const insightsKey = (workflowId: string) => ['insights', workflowId] as const

/** The server reads each running flow every 30 seconds; asking more often returns the same report. */
export const INSIGHTS_REFRESH_MS = 30_000

export function getInsights(workflowId: string, signal?: AbortSignal): Promise<InsightsReport> {
  return requestJson<InsightsReport>(`/api/workflows/${encodeURIComponent(workflowId)}/insights`, { signal })
}

export function useInsights(workflowId: string, enabled: boolean) {
  return useQuery({
    queryKey: insightsKey(workflowId),
    queryFn: ({ signal }) => getInsights(workflowId, signal),
    enabled: enabled && Boolean(workflowId),
    refetchInterval: INSIGHTS_REFRESH_MS,
  })
}
