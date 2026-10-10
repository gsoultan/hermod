import { useQuery } from '@tanstack/react-query'
import { apiFetch } from '@/api'

/**
 * The wire protocols a model server can speak (pkg/ml/inference.Backend), and
 * 'hermod-ml' for a model Hermod trained, served by its own ML worker.
 */
export type MLBackend = 'oip' | 'mlflow' | 'hermod-ml'

/** Whether the model was trained by Hermod rather than registered by address. */
export const isTrainedModel = (m: Pick<MLModel, 'backend'>) => m.backend === 'hermod-ml'

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
  /** How the model is watched; set through saveMonitoring, not saveModel. */
  monitoring?: MLMonitoring
  /** Whether a serving key exists; the key itself is never returned. */
  serving: boolean
  /** When a trained model trains again by itself; absent when it does not. */
  retrain?: RetrainPolicy
  /** How its last retraining went, and the row count the next one counts from. */
  retrain_status?: RetrainStatus
  updated_by?: string
  updated_at?: string
}

/** What a client may set on a model; name and vhost come from the URL. */
export type MLModelInput = Omit<MLModel, 'name' | 'vhost' | 'serving' | 'retrain' | 'retrain_status' | 'updated_by' | 'updated_at' | 'monitoring'>

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

// ---------------------------------------------------------------------------
// Training: datasets on Hermod's ML worker, and the models trained on them.
// ---------------------------------------------------------------------------

export interface WorkerStatus {
  configured: boolean
  ready: boolean
  error?: string
  /**
   * What the worker can train. Absent from a worker older than its
   * capabilities route, which then is assumed to train every algorithm.
   */
  algorithms?: TrainAlgorithm[]
  /** Why each algorithm missing from `algorithms` cannot train there. */
  unavailable?: Partial<Record<TrainAlgorithm, string>>
}

export interface DatasetColumn {
  name: string
  type: 'number' | 'string' | 'bool'
}

export interface DatasetInfo {
  name: string
  rows: number
  columns: DatasetColumn[]
  updated_at: string
  sample?: Record<string, unknown>[]
}

export type TrainTask = 'auto' | 'classification' | 'regression'
export type TrainAlgorithm =
  | 'auto' | 'random_forest' | 'gradient_boosting' | 'linear' | 'xgboost' | 'pytorch_mlp' | 'keras_mlp'

/** The neural-network algorithms: they take TrainParams and need the worker's "-dl" image. */
export const DEEP_ALGORITHMS: readonly TrainAlgorithm[] = ['pytorch_mlp', 'keras_mlp']

export const isDeepAlgorithm = (a: string | null | undefined) => DEEP_ALGORITHMS.includes(a as TrainAlgorithm)

/**
 * Hyperparameters of pytorch_mlp and keras_mlp. An absent field takes the
 * worker's default; the worker checks the bounds.
 */
export interface TrainParams {
  hidden_layers?: number[]
  epochs?: number
  batch_size?: number
  learning_rate?: number
  patience?: number
}

/** The worker's defaults, shown as placeholders. */
export const DEFAULT_TRAIN_PARAMS = { hidden_layers: '64, 32', epochs: '200', batch_size: '32', learning_rate: '0.001', patience: '10' }

/**
 * Reads "64, 32" as layer sizes: [] for an empty field, null when a size is
 * not a whole number above zero.
 */
export function parseHiddenLayers(text: string): number[] | null {
  const parts = text.split(',').map((s) => s.trim()).filter(Boolean)
  const sizes = parts.map(Number)
  return sizes.every((n) => Number.isInteger(n) && n > 0) ? sizes : null
}

export interface GoLive {
  mode: 'never' | 'always' | 'if'
  metric?: string
  min?: number
  max?: number
}

export interface TrainSpec {
  dataset: string
  target: string
  features?: string[]
  task?: TrainTask
  algorithm?: TrainAlgorithm
  /** Only for pytorch_mlp and keras_mlp; the worker refuses it otherwise. */
  params?: TrainParams
  go_live: GoLive
}

export interface ModelVersion {
  model: string
  version: string
  task: string
  algorithm: string
  dataset: string
  target: string
  features: string[]
  metrics: Record<string, number>
  rows: { train: number; test: number }
  created_at: string
  /** Per-feature statistics of the training split, the baseline drift is measured against. */
  feature_stats?: Record<string, FeatureStats>
}

/** One feature of a training split (pkg/ml/worker FeatureStats). */
export interface FeatureStats {
  kind: 'numeric' | 'categorical'
  count: number
  null_fraction: number
  mean?: number
  std?: number
  min?: number
  max?: number
  edges?: number[]
  fractions?: number[]
  top?: Array<{ value: string; fraction: number }>
  other_fraction?: number
}

/**
 * When a trained model trains again by itself (storage.MLRetrainPolicy): on a
 * cron schedule, once its dataset has grown by new_rows rows since it last
 * trained, or either. The go-live rule decides whether the new version serves.
 */
export interface RetrainPolicy {
  schedule?: string
  new_rows?: number
  spec: Omit<TrainSpec, 'go_live'>
  go_live: GoLive
  updated_by?: string
  updated_at?: string
}

/** What a model's last retraining did (storage.MLRetrainStatus). */
export interface RetrainStatus {
  /** Zero time until a retraining has run. */
  at: string
  trigger?: 'schedule' | 'new_rows'
  version?: string
  live: boolean
  reason?: string
  error?: string
  dataset_rows: number
  trained_at: string
}

/** Go's zero time.Time, which the API sends for "never". */
export const isNever = (t: string | undefined) => !t || t.startsWith('0001-01-01')

/** A policy's triggers in words: "Retrains @daily or after 500 new rows". */
export function describeRetrain(p: RetrainPolicy): string {
  const when = [p.schedule?.trim(), p.new_rows ? `after ${p.new_rows} new rows` : ''].filter(Boolean)
  return `Retrains ${when.join(' or ')}`
}

export interface TrainResult {
  model: MLModel
  version: ModelVersion
  live: boolean
  reason: string
}

/** Same rule as the worker's: letters, digits, '_', '.', '-'. */
export const DATASET_NAME_PATTERN = /^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$/

export const TASK_OPTIONS: Array<{ value: TrainTask; label: string }> = [
  { value: 'auto', label: 'Decide from the target' },
  { value: 'classification', label: 'Classify (pick a category)' },
  { value: 'regression', label: 'Predict a number' },
]

export const ALGORITHM_OPTIONS: Array<{ value: TrainAlgorithm; label: string }> = [
  { value: 'auto', label: 'Auto (random forest)' },
  { value: 'random_forest', label: 'Random forest' },
  { value: 'gradient_boosting', label: 'Gradient boosting' },
  { value: 'linear', label: 'Linear / logistic regression' },
  { value: 'xgboost', label: 'XGBoost' },
  { value: 'pytorch_mlp', label: 'PyTorch MLP (neural network)' },
  { value: 'keras_mlp', label: 'Keras MLP (neural network)' },
]

/**
 * The algorithms to offer: the ones the worker says it can train, or all of
 * them when it does not say. "auto" is always offered.
 */
export function algorithmOptions(status: WorkerStatus | undefined) {
  const available = status?.algorithms
  if (!available) return ALGORITHM_OPTIONS
  return ALGORITHM_OPTIONS.filter((o) => o.value === 'auto' || available.includes(o.value))
}

/** The database source types a dataset can be read from. */
export const SQL_SOURCE_TYPES = ['postgres', 'yugabyte', 'mysql', 'mariadb', 'mssql', 'oracle', 'sqlite', 'clickhouse', 'db2']

export const workerStatusKey = ['ml-worker'] as const
export const datasetsKey = (vhost: string) => ['ml-datasets', vhost] as const
export const versionsKey = (vhost: string, name: string) => ['ml-versions', vhost, name] as const

const datasetsUrl = (vhost: string) => `/api/vhosts/${encodeURIComponent(vhost)}/ml/datasets`
const datasetUrl = (vhost: string, name: string) => `${datasetsUrl(vhost)}/${encodeURIComponent(name)}`

export async function getWorkerStatus(signal?: AbortSignal): Promise<WorkerStatus> {
  const res = await apiFetch('/api/ml/worker', { signal, silent: true })
  return (await res.json()) as WorkerStatus
}

export async function listDatasets(vhost: string, signal?: AbortSignal): Promise<DatasetInfo[]> {
  const res = await apiFetch(datasetsUrl(vhost), { signal, silent: true })
  const body = await res.json()
  return (body?.data ?? []) as DatasetInfo[]
}

export async function getDataset(vhost: string, name: string, signal?: AbortSignal): Promise<DatasetInfo> {
  const res = await apiFetch(datasetUrl(vhost, name), { signal, silent: true })
  return (await res.json()) as DatasetInfo
}

/** Replaces a dataset with a CSV or Excel file, sent as the request body. */
export async function uploadDataset(vhost: string, name: string, file: File): Promise<DatasetInfo> {
  const format = file.name.toLowerCase().endsWith('.xlsx') ? 'xlsx' : 'csv'
  const res = await apiFetch(`${datasetUrl(vhost, name)}/file?format=${format}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/octet-stream' },
    body: await file.arrayBuffer(),
    silent: true,
  })
  return (await res.json()) as DatasetInfo
}

export async function datasetFromQuery(
  vhost: string, name: string, sourceId: string, query: string, maxRows?: number,
): Promise<{ name: string; rows: number }> {
  const res = await apiFetch(`${datasetUrl(vhost, name)}/query`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ source_id: sourceId, query, max_rows: maxRows ?? 0 }),
    silent: true,
  })
  return (await res.json()) as { name: string; rows: number }
}

export async function deleteDataset(vhost: string, name: string): Promise<void> {
  await apiFetch(datasetUrl(vhost, name), { method: 'DELETE', silent: true })
}

export async function trainModel(vhost: string, name: string, spec: TrainSpec): Promise<TrainResult> {
  const res = await apiFetch(`${modelUrl(vhost, name)}/train`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(spec),
    silent: true,
  })
  return (await res.json()) as TrainResult
}

export async function listVersions(vhost: string, name: string, signal?: AbortSignal): Promise<ModelVersion[]> {
  const res = await apiFetch(`${modelUrl(vhost, name)}/versions`, { signal, silent: true })
  const body = await res.json()
  return (body?.data ?? []) as ModelVersion[]
}

export async function promoteVersion(vhost: string, name: string, version: string): Promise<MLModel> {
  const res = await apiFetch(`${modelUrl(vhost, name)}/versions/${encodeURIComponent(version)}/promote`, {
    method: 'POST',
    silent: true,
  })
  return (await res.json()) as MLModel
}

export async function setRetrainPolicy(
  vhost: string, name: string, policy: Omit<RetrainPolicy, 'updated_by' | 'updated_at'>,
): Promise<MLModel> {
  const res = await apiFetch(`${modelUrl(vhost, name)}/retrain`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(policy),
    silent: true,
  })
  return (await res.json()) as MLModel
}

export async function clearRetrainPolicy(vhost: string, name: string): Promise<void> {
  await apiFetch(`${modelUrl(vhost, name)}/retrain`, { method: 'DELETE', silent: true })
}

/** The vhost's database sources, for reading a dataset from one. */
export async function listSQLSources(vhost: string, signal?: AbortSignal): Promise<Array<{ id: string; name: string; type: string }>> {
  const res = await apiFetch(`/api/sources?limit=500&vhost=${encodeURIComponent(vhost)}`, { signal, silent: true })
  const body = await res.json()
  const all = (body?.data ?? []) as Array<{ id: string; name: string; type: string }>
  return all.filter((s) => SQL_SOURCE_TYPES.includes(s.type))
}

/** Whether training is available; answers "not configured" on any failure. */
export function useWorkerStatus() {
  return useQuery({
    queryKey: workerStatusKey,
    queryFn: ({ signal }) => getWorkerStatus(signal).catch(() => ({ configured: false, ready: false }) as WorkerStatus),
    retry: false,
    staleTime: 30_000,
  })
}

/** The vhost's datasets, for pickers; an empty list on a refusal. */
export function useVHostDatasets(vhost: string | undefined, enabled = true) {
  return useQuery({
    queryKey: datasetsKey(vhost ?? ''),
    queryFn: ({ signal }) => listDatasets(vhost as string, signal).catch(() => [] as DatasetInfo[]),
    enabled: enabled && isModelVHost(vhost),
    retry: false,
    staleTime: 30_000,
  })
}

/** One dataset's columns and sample, for the target and feature pickers. */
export function useDataset(vhost: string | undefined, name: string | undefined | null) {
  return useQuery({
    queryKey: ['ml-dataset', vhost ?? '', name ?? ''],
    queryFn: ({ signal }) => getDataset(vhost as string, name as string, signal),
    enabled: isModelVHost(vhost) && !!name,
    retry: false,
    staleTime: 30_000,
  })
}

// ---------------------------------------------------------------------------
// Monitoring: the prediction log and input drift.
// ---------------------------------------------------------------------------

/** How a model is watched (internal/storage MLMonitoring). Logging is off until log_sample_rate is above 0. */
export interface MLMonitoring {
  log_sample_rate?: number
  log_mask_fields?: string[]
  log_mask_type?: '' | 'all' | 'partial' | 'email' | 'pii'
  log_retention?: string
  drift_warn?: number
  drift_alert?: number
}

export type CallerKind = 'workflow' | 'rest' | 'grpc' | 'ui'

/** One logged prediction, masked as the model's monitoring said when it was written. */
export interface PredictionLog {
  vhost: string
  model: string
  version?: string
  timestamp: string
  inputs: Record<string, unknown>
  outputs: Record<string, unknown>
  latency_ms: number
  caller_kind: CallerKind
  caller_id?: string
}

export type DriftLevel = 'ok' | 'warn' | 'alert'

export interface FeatureDrift {
  feature: string
  kind: 'numeric' | 'categorical'
  psi: number
  status: DriftLevel
  null_fraction: number
  training_null_fraction: number
}

/** One judged window of live inputs (internal/ml/monitor Report). */
export interface DriftReport {
  vhost: string
  model: string
  version: string
  window_start: string
  window_end: string
  rows: number
  warn: number
  alert: number
  status: DriftLevel
  features: FeatureDrift[]
}

/** The latest drift report, or why there is none yet. */
export interface DriftStatus {
  report: DriftReport | null
  reason?: string
  window?: string
  min_rows?: number
}

export const MASK_TYPE_OPTIONS = [
  { value: 'all', label: 'Replace the whole value (****)' },
  { value: 'partial', label: 'Keep the first and last characters' },
  { value: 'email', label: 'Keep the email domain' },
  { value: 'pii', label: 'Mask PII patterns inside the text' },
]

export const predictionLogsKey = (vhost: string, name: string) => ['ml-prediction-logs', vhost, name] as const
export const driftKey = (vhost: string, name: string) => ['ml-drift', vhost, name] as const

export async function listPredictionLogs(vhost: string, name: string, limit = 100, signal?: AbortSignal): Promise<PredictionLog[]> {
  const res = await apiFetch(`${modelUrl(vhost, name)}/predictions?limit=${limit}`, { signal, silent: true })
  const body = await res.json()
  return (body?.data ?? []) as PredictionLog[]
}

export async function getDrift(vhost: string, name: string, signal?: AbortSignal): Promise<DriftStatus> {
  const res = await apiFetch(`${modelUrl(vhost, name)}/drift`, { signal, silent: true })
  return (await res.json()) as DriftStatus
}

export async function saveMonitoring(vhost: string, name: string, monitoring: MLMonitoring): Promise<MLModel> {
  const res = await apiFetch(`${modelUrl(vhost, name)}/monitoring`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(monitoring),
    silent: true,
  })
  return (await res.json()) as MLModel
}
