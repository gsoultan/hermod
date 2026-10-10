import { useQuery } from '@tanstack/react-query'
import { apiFetch } from '@/api'

/** The wire protocols a model server can speak (pkg/ml/inference.Backend). */
export type MLBackend = 'oip' | 'mlflow'

/** One model as the API returns it (internal/storage MLModel). */
export interface MLModel {
  name: string
  vhost?: string
  description?: string
  backend: MLBackend
  url: string
  remote_model?: string
  remote_version?: string
  token_secret?: string
  input_name?: string
  features?: string[]
  timeout_ms?: number
  /** Whether a serving key exists; the key itself is never returned. */
  serving: boolean
  updated_by?: string
  updated_at?: string
}

/** What a client may set on a model; name and vhost come from the URL. */
export type MLModelInput = Omit<MLModel, 'name' | 'vhost' | 'serving' | 'updated_by' | 'updated_at'>

/** The rule storage.ValidMLModelName applies. */
export const MODEL_NAME_PATTERN = /^[A-Za-z][A-Za-z0-9_-]{0,63}$/

export const modelNameRule = 'Starts with a letter; letters, digits, "_" and "-" after that.'

/** A vhost a model can belong to: a real one, not the "every vhost" filter. */
export function isModelVHost(vhost: string | undefined | null): vhost is string {
  return !!vhost && vhost !== 'all'
}

export const mlModelsKey = (vhost: string) => ['ml-models', vhost] as const

const modelsUrl = (vhost: string) => `/api/vhosts/${encodeURIComponent(vhost)}/ml/models`
const modelUrl = (vhost: string, name: string) => `${modelsUrl(vhost)}/${encodeURIComponent(name)}`

/** The public endpoint an application calls with the model's serving key. */
export const servingUrl = (vhost: string, name: string) =>
  `/api/ml/serve/${encodeURIComponent(vhost)}/${encodeURIComponent(name)}`

export async function listModels(vhost: string, signal?: AbortSignal): Promise<MLModel[]> {
  const res = await apiFetch(modelsUrl(vhost), { signal, silent: true })
  const body = await res.json()
  return (body?.data ?? []) as MLModel[]
}

export async function saveModel(vhost: string, name: string, model: MLModelInput): Promise<MLModel> {
  const res = await apiFetch(modelUrl(vhost, name), {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(model),
    silent: true,
  })
  return (await res.json()) as MLModel
}

export async function deleteModel(vhost: string, name: string): Promise<void> {
  await apiFetch(modelUrl(vhost, name), { method: 'DELETE', silent: true })
}

export async function predict(vhost: string, name: string, instances: Record<string, unknown>[]): Promise<unknown[]> {
  const res = await apiFetch(`${modelUrl(vhost, name)}/predict`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ instances }),
    silent: true,
  })
  const body = await res.json()
  return (body?.predictions ?? []) as unknown[]
}

/** Makes a new serving key. This response is the only time it can be read. */
export async function rotateServingKey(vhost: string, name: string): Promise<string> {
  const res = await apiFetch(`${modelUrl(vhost, name)}/serving-key`, { method: 'POST', silent: true })
  const body = await res.json()
  return body?.key as string
}

export async function disableServing(vhost: string, name: string): Promise<void> {
  await apiFetch(`${modelUrl(vhost, name)}/serving-key`, { method: 'DELETE', silent: true })
}

/**
 * The vhost's models, for the Predict node's picker. Answers an empty list on
 * a refusal, as useVHostSecretNames does: to the person building a workflow
 * there is simply nothing to pick.
 */
export function useVHostModels(vhost: string | undefined) {
  return useQuery({
    queryKey: mlModelsKey(vhost ?? ''),
    queryFn: ({ signal }) => listModels(vhost as string, signal).catch(() => [] as MLModel[]),
    enabled: isModelVHost(vhost),
    retry: false,
    staleTime: 30_000,
  })
}
