import { apiFetch } from '@/api'

/** A model's price per million tokens (internal/storage AIModelPrice). "*" prices every other model. */
export interface AIModelPrice {
  model: string
  input_per_million: number
  output_per_million: number
}

/** One workflow's monthly cap (internal/storage AIWorkflowCap). Zero is no limit. */
export interface AIWorkflowCap {
  workflow_id: string
  monthly_tokens?: number
  monthly_cost?: number
}

/** A vhost's AI spending policy (internal/storage AIBudget). Zero is no limit. */
export interface AIBudget {
  vhost?: string
  disabled: boolean
  monthly_tokens?: number
  monthly_cost?: number
  currency?: string
  prices?: AIModelPrice[]
  workflows?: AIWorkflowCap[]
  updated_by?: string
  updated_at?: string
}

/** What one scope spent in the month (internal/storage AIUsage). Cost is in millionths. */
export interface AIUsage {
  vhost: string
  period: string
  workflow_id?: string
  calls: number
  input_tokens: number
  output_tokens: number
  cost_micros: number
}

/** GET /api/vhosts/{vhost}/ai/budget (internal/aibudget Report). */
export interface AIBudgetReport {
  vhost: string
  period: string
  budget: AIBudget
  usage: AIUsage
  workflows: AIUsage[]
}

/** The price entry for models the list does not name (storage.AnyModel). */
export const ANY_MODEL = '*'

/** When the alert goes out and the bar turns: 80% of a limit. */
export const WARN_RATIO = 0.8

export const aiBudgetKey = (vhost: string) => ['ai-budget', vhost] as const

const budgetUrl = (vhost: string) => `/api/vhosts/${encodeURIComponent(vhost)}/ai/budget`

export async function getAIBudget(vhost: string, signal?: AbortSignal): Promise<AIBudgetReport> {
  const res = await apiFetch(budgetUrl(vhost), { signal, silent: true })
  return (await res.json()) as AIBudgetReport
}

export async function saveAIBudget(vhost: string, budget: AIBudget): Promise<AIBudgetReport> {
  const res = await apiFetch(budgetUrl(vhost), {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(budget),
    silent: true,
  })
  return (await res.json()) as AIBudgetReport
}

export async function setAIKillSwitch(vhost: string, disabled: boolean): Promise<AIBudgetReport> {
  const res = await apiFetch(`/api/vhosts/${encodeURIComponent(vhost)}/ai/kill-switch`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ disabled }),
    silent: true,
  })
  return (await res.json()) as AIBudgetReport
}

export const usedTokens = (u: AIUsage | undefined) => (u ? u.input_tokens + u.output_tokens : 0)

/** Share of a limit used, or null when there is no limit. */
export function share(used: number, limit: number | undefined): number | null {
  if (!limit || limit <= 0) return null
  return used / limit
}

/** The state a share of a limit is in, as the page colours it. */
export function level(ratio: number | null): 'none' | 'ok' | 'warn' | 'over' {
  if (ratio === null) return 'none'
  if (ratio >= 1) return 'over'
  if (ratio >= WARN_RATIO) return 'warn'
  return 'ok'
}

export const costOf = (micros: number) => micros / 1e6
