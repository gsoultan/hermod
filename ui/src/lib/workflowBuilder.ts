import { requestJson } from '@/lib/executions'

/**
 * The natural-language workflow builder:
 * internal/workflow/builder/transport/http/handler.go.
 */

/** builder.MaxDescriptionLength. */
export const MAX_DESCRIPTION_LENGTH = 4000

export interface BuilderConnection {
  provider: string
  model: string
  /** Always a {{secret("NAME")}} reference; the server refuses a plain key. */
  apiKey: string
  baseUrl: string
}

export interface BuildRequest {
  description: string
  vhost: string
  connection: BuilderConnection
}

/** builder.Issue. */
export interface BuilderIssue {
  severity: string
  message: string
  recommendation: string
  node_id?: string
}

/** A workflow as storage.Workflow serialises it; only the fields the preview reads. */
export interface DraftWorkflow {
  id?: string
  name: string
  vhost: string
  active: boolean
  nodes?: { id: string; type: string; ref_id?: string; x?: number; y?: number; config?: Record<string, unknown> }[]
  edges?: { id: string; source_id: string; target_id: string; source_handle?: string; target_handle?: string }[]
  [key: string]: unknown
}

export interface BuildResponse {
  workflow: DraftWorkflow
  issues: BuilderIssue[] | null
  /** Always false: the draft is never saved by the builder. */
  saved: boolean
  provider: string
  model: string
  usage: { input_tokens: number; output_tokens: number }
}

export function buildWorkflow(req: BuildRequest): Promise<BuildResponse> {
  return requestJson<BuildResponse>('/api/ai/build-workflow', { method: 'POST', body: JSON.stringify(req) })
}
