import { useQuery } from '@tanstack/react-query'
import { apiFetch } from '@/api'
import type { SourceColumn } from './mlQueryBuilder'

/**
 * The wire protocols a model server can speak (pkg/ml/inference.Backend):
 * 'mimir' for a mimir-server, 'oip' for Open Inference, 'mlflow' for an
 * MLflow scoring server; and 'hermod-ml' for a model Hermod trained, served
 * by its own ML worker.
 */
export type MLBackend = 'mimir' | 'oip' | 'mlflow' | 'hermod-ml'

/** What each kind of server is called where a model's kind is shown. */
export const BACKEND_LABEL: Record<MLBackend, string> = {
  mimir: 'mimir-server', oip: 'Open Inference', mlflow: 'MLflow', 'hermod-ml': 'Trained in Hermod',
}

/** One model as a mimir server lists it (pkg/ml/inference.RemoteModel). */
export interface MimirModel {
  id: string
  name: string
  architecture?: string
  description?: string
  /** Whether it answers now; status says why not when it does not. */
  ready: boolean
  status?: string
  accuracy?: number
  labels?: string[]
  columns?: Array<{ name: string; kind?: string }>
}

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
  /** feature -> number, string or bool, where known (a model trained here). */
  feature_types?: Record<string, string>
  timeout_ms?: number
  /** Offered to MCP clients as a predict_<name> tool; set with setMCPExposure. */
  mcp_exposed?: boolean
  /** How the model is watched; set through saveMonitoring, not saveModel. */
  monitoring?: MLMonitoring
  /** Whether a serving key exists; the key itself is never returned. */
  serving: boolean
  /** When a trained model trains again by itself; absent when it does not. */
  retrain?: RetrainPolicy
  /** How its last retraining went, and the row count the next one counts from. */
  retrain_status?: RetrainStatus
  /** Where a trained model is scored; absent means the ML worker. */
  scoring?: Scoring
  /** What a trained model does: classification, regression or recommendation. */
  task?: string
  updated_by?: string
  updated_at?: string
}

/** What a client may set on a model; name and vhost come from the URL. */
export type MLModelInput = Omit<
  MLModel,
  | 'name'
  | 'vhost'
  | 'serving'
  | 'retrain'
  | 'retrain_status'
  | 'scoring'
  | 'updated_by'
  | 'updated_at'
  | 'mcp_exposed'
  | 'feature_types'
  | 'monitoring'
  | 'task'
>

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

/**
 * The models a mimir server offers the API key held in the vhost's secret
 * `tokenSecret`, for the Connect form to pick from. The key stays on the
 * server side; the browser only ever names the secret.
 */
export async function listMimirModels(vhost: string, url: string, tokenSecret: string, signal?: AbortSignal): Promise<MimirModel[]> {
  const res = await apiFetch(`/api/vhosts/${encodeURIComponent(vhost)}/ml/mimir/models`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ url, token_secret: tokenSecret }),
    signal,
    silent: true,
  })
  const body = await res.json()
  return (body?.models ?? []) as MimirModel[]
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

/** A recommendation model's task. */
export const RECOMMENDATION = 'recommendation'

export const isRecommender = (m: Pick<MLModel, 'task'> | undefined | null) => m?.task === RECOMMENDATION

/** One recommended item (pkg/ml/recommend Item); reason 'popular' marks the fallback. */
export interface RecommendedItem {
  item: string
  score: number
  reason?: string
}

/** A recommendation call: a person's top items, or the items like one item. */
export type RecommendCall =
  | { user: string | number; k?: number; exclude_seen?: boolean }
  | { item: string | number; k?: number }

/** Asks a recommendation model through the same predict route as rows. */
export async function recommend(vhost: string, name: string, call: RecommendCall): Promise<RecommendedItem[]> {
  const res = await apiFetch(`${modelUrl(vhost, name)}/predict`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(call),
    silent: true,
  })
  const body = await res.json()
  return (body?.items ?? []) as RecommendedItem[]
}

/** Makes a new serving key. This response is the only time it can be read. */
export async function rotateServingKey(vhost: string, name: string): Promise<string> {
  const res = await apiFetch(`${modelUrl(vhost, name)}/serving-key`, { method: 'POST', silent: true })
  const body = await res.json()
  return body?.key as string
}

/** Offers the model to MCP clients as a predict tool, or stops. */
export async function setMCPExposure(vhost: string, name: string, exposed: boolean): Promise<MLModel> {
  const res = await apiFetch(`${modelUrl(vhost, name)}/mcp`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ exposed }),
    silent: true,
  })
  return (await res.json()) as MLModel
}

/** The ML quotas, by their API names (internal/storage MLQuotas). */
export const QUOTA_KEYS = [
  'max_datasets',
  'max_dataset_rows',
  'max_dataset_bytes',
  'max_models',
  'max_concurrent_trainings',
  'max_predictions_per_second',
] as const
export type QuotaKey = (typeof QUOTA_KEYS)[number]

/** A vhost's own quotas: null falls back to the server default, 0 is no limit. */
export type MLQuotaSettings = Record<QuotaKey, number | null>

export interface MLQuotasView {
  quotas: MLQuotaSettings & { updated_by?: string }
  defaults: MLQuotaSettings
  /** The limits in force; 0 is no limit. */
  effective: Record<QuotaKey, number>
}

export const mlQuotasKey = (vhost: string) => ['ml-quotas', vhost] as const
const quotasUrl = (vhost: string) => `/api/vhosts/${encodeURIComponent(vhost)}/ml/quotas`

export async function getQuotas(vhost: string, signal?: AbortSignal): Promise<MLQuotasView> {
  const res = await apiFetch(quotasUrl(vhost), { signal, silent: true })
  return (await res.json()) as MLQuotasView
}

/** Replaces the vhost's quotas. Administrators only. */
export async function putQuotas(vhost: string, quotas: MLQuotaSettings): Promise<MLQuotasView> {
  const res = await apiFetch(quotasUrl(vhost), {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(quotas),
    silent: true,
  })
  return (await res.json()) as MLQuotasView
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

/** The training options this server offers (internal/ml Capabilities). */
export interface WorkerCapabilities {
  /** Custom scripts are on and their worker pool is configured: they can train. */
  custom_scripts: boolean
  /** Custom scripts are on, pool or not: they can be saved and read. */
  scripts_enabled: boolean
  /** A GPU worker pool is configured. */
  gpu: boolean
}

/**
 * Where the worker in force was configured (pkg/ml/worker Source): "builtin"
 * is Hermod's own engine alone, when no hermod-ml worker is configured.
 */
export type WorkerSource = 'environment' | 'saved' | 'builtin' | 'none'

/** The engine that trained, and serves, a version or holds a dataset. */
export type MLEngine = 'builtin' | 'worker'

/**
 * Each engine in force (internal/ml/transport/http WorkerStatus): Hermod's
 * built-in one, and the hermod-ml worker with whether it answers. Both run
 * at once; each version is served by the engine that trained it.
 */
export interface WorkerEngines {
  builtin: { available: boolean; algorithms?: TrainAlgorithm[] }
  worker: {
    configured: boolean
    ready: boolean
    source?: WorkerSource
    error?: string
    algorithms?: TrainAlgorithm[]
    unavailable?: Partial<Record<TrainAlgorithm, string>>
  }
}

export interface WorkerStatus {
  configured: boolean
  ready: boolean
  /** Absent from a server older than connecting the worker from the console. */
  source?: WorkerSource
  error?: string
  capabilities?: WorkerCapabilities
  /**
   * What the worker can train. Absent from a worker older than its
   * capabilities route, which then is assumed to train every algorithm.
   */
  algorithms?: TrainAlgorithm[]
  /** Why each algorithm missing from `algorithms` cannot train there. */
  unavailable?: Partial<Record<TrainAlgorithm, string>>
  /** Each engine; absent from a server older than running both at once. */
  engines?: WorkerEngines
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
  /** The engine that holds it; absent from an older server. */
  engine?: MLEngine
}

export type TrainTask = 'auto' | 'classification' | 'regression' | 'recommendation'

/** The recommendation algorithms (ml/worker hermod_ml/recommend.py). */
export type RecommendAlgorithm = 'auto' | 'als' | 'item_similarity'

export const RECOMMEND_ALGORITHM_OPTIONS: Array<{ value: RecommendAlgorithm; label: string }> = [
  { value: 'auto', label: 'Auto (tries both, keeps the better)' },
  { value: 'als', label: 'Matrix factorization (ALS)' },
  { value: 'item_similarity', label: 'Item similarity (people who had this also had…)' },
]

/** The columns a recommendation model learns from. */
export interface RecommendColumns {
  /** Who: a person or account id. */
  user: string
  /** What they had: a product, article or song id. */
  item: string
  /** How strongly, e.g. quantity or rating; empty counts every row as 1. */
  strength?: string
  /** When; the latest of each person is held back to test on. */
  time?: string
}

/**
 * How a recommendation version did, in words: out of 100 people, how many
 * found something they really picked next in their top 10, against simply
 * recommending everyone the most popular items.
 */
export function describeRecommendation(metrics: Record<string, number>): { hits: string; baseline?: string; coverage?: string } {
  const per100 = (v: number | undefined) => Math.round((v ?? 0) * 100)
  const hits = per100(metrics['hit_rate@10'])
  const out: { hits: string; baseline?: string; coverage?: string } = {
    hits: `For ${hits} of 100 people, one of their top 10 was something they really picked next.`,
  }
  if (metrics['baseline_hit_rate@10'] !== undefined) {
    const base = per100(metrics['baseline_hit_rate@10'])
    const verdict = hits > base ? 'better than' : hits === base ? 'the same as' : 'worse than'
    out.baseline = `Recommending everyone the most popular items would find it for ${base} of 100: this model is ${verdict} that.`
  }
  if (metrics.coverage !== undefined) {
    out.coverage = `Its top-10 lists used ${per100(metrics.coverage)}% of the items.`
  }
  return out
}
/** A built-in algorithm, or `custom:<script>` for one of the vhost's training scripts. */
export type TrainAlgorithm =
  | 'auto' | 'random_forest' | 'gradient_boosting' | 'extra_trees' | 'hist_gradient_boosting' | 'linear' | 'mlp' | 'xgboost'
  | 'pytorch_mlp' | 'keras_mlp' | `custom:${string}`
export type TrainDevice = 'cpu' | 'gpu'
/** How much effort "auto" spends trying algorithms; the worker refuses it with any other algorithm. */
export type TrainPreset = 'quick' | 'balanced' | 'best'

/** The neural-network algorithms: they take TrainParams and need the worker's "-dl" image. */
export const DEEP_ALGORITHMS: readonly TrainAlgorithm[] = ['pytorch_mlp', 'keras_mlp']

export const isDeepAlgorithm = (a: string | null | undefined) => DEEP_ALGORITHMS.includes(a as TrainAlgorithm)

/** The built-in engine's neural network: it takes TrainParams and needs no worker. */
export const BUILTIN_MLP: TrainAlgorithm = 'mlp'

/** Whether `a` is a neural network, and so takes TrainParams. */
export const takesTrainParams = (a: string | null | undefined) => a === BUILTIN_MLP || isDeepAlgorithm(a)

/**
 * Hyperparameters of mlp, pytorch_mlp and keras_mlp. An absent field takes the
 * engine's default; the engine checks the bounds.
 */
export interface TrainParams {
  hidden_layers?: number[]
  epochs?: number
  batch_size?: number
  learning_rate?: number
  patience?: number
  /** How the trained network keeps its weights; auto keeps int8 when it scores within a tolerance of float32. */
  precision?: TrainPrecision
  /** CPU threads the training uses, 0 to 256; absent or 0 for every CPU but one. */
  threads?: number
}

export type TrainPrecision = 'auto' | 'float32' | 'int8'

export const PRECISION_OPTIONS: { value: TrainPrecision; label: string }[] = [
  { value: 'auto', label: 'auto' },
  { value: 'float32', label: 'float32' },
  { value: 'int8', label: 'int8' },
]

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
  /** 'gpu' trains on the GPU worker pool; empty or 'cpu' on the main worker. */
  device?: TrainDevice
  /** Only for mlp, pytorch_mlp and keras_mlp; the engine refuses it otherwise. */
  params?: TrainParams
  /** Only with task 'recommendation', whose target is empty. */
  recommend?: RecommendColumns
  /** Only with algorithm "auto"; the server takes "balanced" when it is absent. */
  preset?: TrainPreset
  go_live: GoLive
}

/** One algorithm "auto" tried, scored by cross-validation on the training split. */
export interface LeaderboardEntry {
  algorithm: string
  score: number
  /** "balanced_accuracy" to classify, "r2" for numbers. */
  metric: string
  folds: number
  seconds: number
  /** The one the version was trained with. */
  chosen: boolean
}

/** How much a feature mattered on the held-out rows; the weights sum to 1. */
export interface FeatureImportance {
  feature: string
  weight: number
}

export interface ModelVersion {
  model: string
  version: string
  /** The engine that trained it and serves it; absent from an older server. */
  engine?: MLEngine
  task: string
  algorithm: string
  dataset: string
  target: string
  features: string[]
  metrics: Record<string, number>
  rows: { train: number; test: number }
  created_at: string
  /** The custom script that trained the version, when one did. */
  script?: { name: string; sha256: string }
  /** Per-feature statistics of the training split, the baseline drift is measured against. */
  feature_stats?: Record<string, FeatureStats>
  /** A recommendation version's columns. */
  recommend?: RecommendColumns
  /** How many people and items a recommendation version knows. */
  catalog?: { users: number; items: number }
  /** A few people of the test: what they were recommended and what they picked next. */
  samples?: Array<{ user: string; recommended: string[]; picked: string[] }>
  /** With algorithm auto: each algorithm tried and its NDCG@10. */
  candidates?: Record<string, number>
  /** The quality an "auto" training used. Absent from older servers, as are the fields below. */
  preset?: TrainPreset
  /** What "auto" tried, best first. */
  leaderboard?: LeaderboardEntry[]
  /** The top features by importance, most important first. */
  importance?: FeatureImportance[]
  /** The metrics of a model that always guesses the most common class, or the mean. */
  baseline?: Record<string, number>
  /** Notes about the data found while training, in plain words. */
  warnings?: string[]
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
  { value: 'auto', label: 'Auto (tries several and keeps the best)' },
  { value: 'random_forest', label: 'Random forest' },
  { value: 'gradient_boosting', label: 'Gradient boosting' },
  { value: 'extra_trees', label: 'Extra trees' },
  { value: 'hist_gradient_boosting', label: 'Histogram gradient boosting' },
  { value: 'linear', label: 'Linear / logistic regression' },
  { value: 'mlp', label: 'Neural network (built-in)' },
  { value: 'xgboost', label: 'XGBoost' },
  { value: 'pytorch_mlp', label: 'PyTorch MLP (neural network)' },
  { value: 'keras_mlp', label: 'Keras MLP — TensorFlow (neural network)' },
]

/** An algorithm's name for people: its option label, or the name itself. */
export function algorithmLabel(algorithm: string): string {
  if (algorithm === 'auto') return 'Auto'
  return ALGORITHM_OPTIONS.find((o) => o.value === algorithm)?.label ?? algorithm
}

/** The qualities "auto" trains at, with what each costs, for the pickers. */
export const PRESET_OPTIONS: Array<{ value: TrainPreset; label: string; description: string; time: string }> = [
  { value: 'quick', label: 'Quick', description: 'Tries one or two fast methods. Good for a first look.', time: 'About a minute' },
  {
    value: 'balanced', label: 'Balanced', description: 'Tries four methods, checks each three times, and keeps the best.',
    time: 'A few minutes',
  },
  {
    value: 'best', label: 'Best quality', description: 'Tries every method it has and tunes them. Worth it before going live.',
    time: '10 minutes or more',
  },
]

export const DEFAULT_PRESET: TrainPreset = 'balanced'

export const DEVICE_OPTIONS: Array<{ value: TrainDevice; label: string }> = [
  { value: 'cpu', label: 'CPU' },
  { value: 'gpu', label: 'GPU' },
]

/** The algorithm that trains with a custom script. */
export const CUSTOM_PREFIX = 'custom:'
export const isCustomAlgorithm = (a: string | undefined | null) => !!a && a.startsWith(CUSTOM_PREFIX)

/** Why a neural network cannot be picked on a worker that runs the slim image. */
export const DEEP_IMAGE_REASON = 'Needs the deep-learning worker image (hermod-ml:…-dl).'

/** One choice of the Algorithm pickers; a disabled one says why in `description`. */
export interface AlgorithmOption {
  value: string
  label: string
  disabled?: boolean
  description?: string
}

/**
 * Whether the worker can train `algorithm`: every one, when it does not say,
 * but the built-in engine's network only where it is listed.
 */
export function canTrainAlgorithm(status: WorkerStatus | undefined, algorithm: TrainAlgorithm): boolean {
  const available = status?.algorithms
  if (algorithm === 'auto') return true
  if (!available) return algorithm !== BUILTIN_MLP
  return available.includes(algorithm)
}

/** Whether Hermod's built-in engine is the only engine in force. */
export const isBuiltinEngine = (status: WorkerStatus | undefined) => status?.source === 'builtin'

/** Whether Hermod's built-in engine is in force, alone or beside a worker. */
export const hasBuiltinEngine = (status: WorkerStatus | undefined) =>
  status?.engines?.builtin.available ?? isBuiltinEngine(status)

/** The hermod-ml worker beside the built-in engine, or undefined when that is not in force. */
export const workerBesideBuiltin = (status: WorkerStatus | undefined) =>
  hasBuiltinEngine(status) ? status?.engines?.worker : undefined

/** Why an algorithm cannot be picked on the built-in engine (pkg/ml/builtin NeedsWorker). */
export const BUILTIN_OFF_REASON = 'Needs the hermod-ml worker.'

/** Whether a ready worker can train the neural networks (PyTorch and TensorFlow). */
export function hasDeepLearning(status: WorkerStatus | undefined): boolean {
  return !!status?.ready && DEEP_ALGORITHMS.some((a) => canTrainAlgorithm(status, a))
}

/**
 * The algorithms a training can pick: the ones the engine says it can train
 * (all of them when it does not say; "auto" always), then the vhost's scripts
 * when the server can train with them. On the built-in engine, the
 * algorithms only the hermod-ml worker trains stay in the list, disabled and
 * saying so; on a worker, so do the neural networks it cannot train, so that
 * someone looking for TensorFlow finds what it takes.
 */
export function algorithmOptions(status: WorkerStatus | undefined, scripts: MLScript[] = []): AlgorithmOption[] {
  const builtIn = ALGORITHM_OPTIONS.flatMap((o): AlgorithmOption[] => {
    if (canTrainAlgorithm(status, o.value)) return [o]
    if (hasBuiltinEngine(status) && status?.unavailable?.[o.value] && !(isDeepAlgorithm(o.value) && status?.engines?.worker.ready)) {
      return [{ ...o, disabled: true, description: BUILTIN_OFF_REASON }]
    }
    return isDeepAlgorithm(o.value) ? [{ ...o, disabled: true, description: DEEP_IMAGE_REASON }] : []
  })
  const custom = status?.capabilities?.custom_scripts
    ? scripts.map((s) => ({ value: `${CUSTOM_PREFIX}${s.name}`, label: `Script: ${s.name}` }))
    : []
  return [...builtIn, ...custom]
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

// ---------------------------------------------------------------------------
// Connecting the worker from the console (administrators only).
// ---------------------------------------------------------------------------

/** The saved worker connection; the token never comes back, only token_set. */
export interface WorkerConnection {
  source: WorkerSource
  url: string
  token_set: boolean
  /** False when the server's environment sets the worker, which wins. */
  editable: boolean
}

/** What Test connection found (pkg/ml/worker ProbeReport). */
export type ProbeResult = 'ok' | 'bad_address' | 'unreachable' | 'not_a_worker' | 'unauthorized' | 'error'

export interface ProbeReport {
  result: ProbeResult
  message: string
  deep_learning: boolean
  frameworks?: string[]
  algorithms?: string[]
}

export const workerConnectionKey = ['ml-worker-connection'] as const
const workerConnectionUrl = '/api/ml/worker/connection'

export async function getWorkerConnection(signal?: AbortSignal): Promise<WorkerConnection> {
  const res = await apiFetch(workerConnectionUrl, { signal, silent: true })
  return (await res.json()) as WorkerConnection
}

/** The body of a test or a save: no token keeps (or tests with) the saved one. */
const connectionBody = (url: string, token?: string) => JSON.stringify(token === undefined ? { url } : { url, token })

export async function testWorkerConnection(url: string, token?: string): Promise<ProbeReport> {
  const res = await apiFetch(`${workerConnectionUrl}/test`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: connectionBody(url, token), silent: true,
  })
  return (await res.json()) as ProbeReport
}

export async function saveWorkerConnection(url: string, token?: string): Promise<WorkerConnection> {
  const res = await apiFetch(workerConnectionUrl, {
    method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: connectionBody(url, token), silent: true,
  })
  return (await res.json()) as WorkerConnection
}

/**
 * The hermod-ml image tag for this Hermod: its own release (the two are
 * released together, .github/workflows/release.yml), or latest for a build
 * that is not a release; "-dl" for the deep-learning image.
 */
export function workerImageTag(version: string | undefined, deep: boolean): string {
  const head = (version ?? '').trim().split(/\s+/)[0]
  const release = head.match(/^v?(\d+\.\d+\.\d+(?:-(?:rc|alpha|beta)\.\d+)?)$/)?.[1]
  return `${release ?? 'latest'}${deep ? '-dl' : ''}`
}

/** A random worker token: 32 bytes, as hex. */
export function generateWorkerToken(): string {
  const bytes = new Uint8Array(32)
  crypto.getRandomValues(bytes)
  return Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('')
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

/**
 * Replaces a dataset with a CSV, Excel or Parquet file, sent as the request
 * body; the format is the file's extension, and anything else is sent as CSV
 * for the server to judge.
 */
export async function uploadDataset(vhost: string, name: string, file: File): Promise<DatasetInfo> {
  const ext = file.name.toLowerCase().split('.').pop()
  const format = ext === 'xlsx' || ext === 'parquet' ? ext : 'csv'
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

const sourceUrl = (vhost: string, id: string) =>
  `/api/vhosts/${encodeURIComponent(vhost)}/ml/sources/${encodeURIComponent(id)}`

/** The tables of one of the vhost's database sources, for the query builder. */
export async function listSourceTables(vhost: string, sourceId: string, signal?: AbortSignal): Promise<string[]> {
  const res = await apiFetch(`${sourceUrl(vhost, sourceId)}/tables`, { signal, silent: true })
  const body = await res.json()
  return Array.isArray(body) ? (body as string[]) : []
}

/** The columns of a table of one of the vhost's database sources. */
export async function listSourceColumns(vhost: string, sourceId: string, table: string, signal?: AbortSignal): Promise<SourceColumn[]> {
  const res = await apiFetch(`${sourceUrl(vhost, sourceId)}/columns?table=${encodeURIComponent(table)}`, { signal, silent: true })
  const body = await res.json()
  return Array.isArray(body) ? (body as SourceColumn[]) : []
}

/** The first rows a dataset query returns; `more` says it returns more. */
export interface QueryPreview {
  columns: string[]
  rows: Array<Record<string, unknown>>
  more: boolean
}

/** Runs a dataset query on the source and returns its first rows, filling nothing. */
export async function previewQuery(vhost: string, sourceId: string, query: string, limit = 20): Promise<QueryPreview> {
  const res = await apiFetch(`${sourceUrl(vhost, sourceId)}/preview`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ query, limit }),
    silent: true,
  })
  return (await res.json()) as QueryPreview
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

// ---------------------------------------------------------------------------
// Asynchronous jobs: a predict, recommend or train call sent with
// "respond": "async" is answered 202 with a job to follow.
// ---------------------------------------------------------------------------

export type MLJobKind = 'predict' | 'recommend' | 'train'
export type MLJobStatus = 'queued' | 'running' | 'succeeded' | 'failed' | 'cancelled'

export const JOB_STATUSES: readonly MLJobStatus[] = ['queued', 'running', 'succeeded', 'failed', 'cancelled']

/** A job as GET .../ml/jobs/{id} shows it; listings leave out request and result. */
export interface MLJob {
  id: string
  vhost: string
  model: string
  kind: MLJobKind
  status: MLJobStatus
  /** 0 to 1. */
  progress: number
  progress_message?: string
  request?: unknown
  /** The answer, when small enough to keep with the job; else read from result_url. */
  result?: unknown
  result_ref?: string
  error?: string
  callback_url?: string
  callback_status?: string
  cancel_requested?: boolean
  created_by?: string
  created_at: string
  started_at?: string
  finished_at?: string
  expires_at: string
  status_url: string
  result_url?: string
}

/** What a call sent with "respond": "async" is answered with (202). */
export interface AcceptedJob {
  job_id: string
  status: MLJobStatus
  status_url: string
}

/** Whether a job has ended, one way or another. */
export const jobFinished = (s: MLJobStatus | undefined) => s === 'succeeded' || s === 'failed' || s === 'cancelled'

const jobsUrl = (vhost: string) => `/api/vhosts/${encodeURIComponent(vhost)}/ml/jobs`
const jobUrl = (vhost: string, id: string) => `${jobsUrl(vhost)}/${encodeURIComponent(id)}`

export const mlJobsKey = (vhost: string) => ['ml-jobs', vhost] as const
export const mlJobKey = (vhost: string, id: string) => ['ml-job', vhost, id] as const

/**
 * Starts training as a job and returns it to follow. A server older than
 * jobs trains while the request waits and answers with the result instead,
 * which comes back as `result`.
 */
export async function startTraining(vhost: string, name: string, spec: TrainSpec):
Promise<{ job: AcceptedJob; result?: undefined } | { result: TrainResult; job?: undefined }> {
  const res = await apiFetch(`${modelUrl(vhost, name)}/train`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Prefer: 'respond-async' },
    body: JSON.stringify({ ...spec, respond: 'async' }),
    silent: true,
  })
  const body = await res.json()
  if (res.status === 202 && body?.job_id) return { job: body as AcceptedJob }
  return { result: body as TrainResult }
}

export async function getJob(vhost: string, id: string, signal?: AbortSignal): Promise<MLJob> {
  const res = await apiFetch(jobUrl(vhost, id), { signal, silent: true })
  return (await res.json()) as MLJob
}

/** A succeeded job's answer, read from the job or, when kept apart, from its result URL. */
export async function jobResult<T>(vhost: string, job: MLJob, signal?: AbortSignal): Promise<T> {
  if (job.result !== undefined && job.result !== null) return job.result as T
  const res = await apiFetch(`${jobUrl(vhost, job.id)}/result`, { signal, silent: true })
  return (await res.json()) as T
}

export async function listJobs(vhost: string, filter: { model?: string; status?: string; limit?: number; offset?: number } = {},
  signal?: AbortSignal): Promise<{ data: MLJob[]; total: number }> {
  const q = new URLSearchParams()
  for (const [k, v] of Object.entries(filter)) if (v !== undefined && v !== '') q.set(k, String(v))
  const res = await apiFetch(`${jobsUrl(vhost)}${q.size ? `?${q}` : ''}`, { signal, silent: true })
  const body = await res.json()
  return { data: (body?.data ?? []) as MLJob[], total: body?.total ?? 0 }
}

export async function cancelJob(vhost: string, id: string): Promise<MLJob> {
  const res = await apiFetch(`${jobUrl(vhost, id)}/cancel`, { method: 'POST', silent: true })
  return (await res.json()) as MLJob
}

/** What the pre-flight check says about a target column (POST .../ml/train-check). */
export type TargetKind = 'yes_no' | 'category' | 'number'
export type ColumnRole = 'use' | 'id_like' | 'constant' | 'mostly_empty' | 'leak_suspect' | 'target'

/** The roles of columns that are left out of training unless a person puts them back. */
export const LEAVE_OUT_ROLES: readonly ColumnRole[] = ['id_like', 'constant', 'mostly_empty', 'leak_suspect']

export interface TrainCheck {
  task: 'classification' | 'regression'
  target_kind: TargetKind
  /** Only to classify. */
  classes?: number
  rows: number
  usable_rows: number
  columns: Array<{ name: string; type: string; role: ColumnRole; note?: string }>
  warnings: Array<{ code: string; message: string; column?: string }>
  /** False only when training would be refused; the warnings say why. */
  ok: boolean
}

/**
 * Checks a training before it runs: reads the dataset and trains nothing.
 * Answers null from a server older than the check, which has no such route.
 */
export async function trainCheck(vhost: string, spec: TrainSpec, signal?: AbortSignal): Promise<TrainCheck | null> {
  try {
    const res = await apiFetch(`/api/vhosts/${encodeURIComponent(vhost)}/ml/train-check`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(spec),
      signal,
      silent: true,
    })
    return (await res.json()) as TrainCheck
  } catch (error) {
    const status = (error as { status?: number }).status
    if (status === 404 || status === 405 || status === 501) return null
    throw error
  }
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

/** Where a trained model's predictions are computed (storage.MLScoring*). */
export type Scoring = 'worker' | 'in_process'

/** How a model is scored now (ml.ScoringStatus). */
export interface ScoringStatus {
  scoring: Scoring
  /** Whether the live version is scored in-process right now. */
  in_process: boolean
  version?: string
  /** The operators of the live version's graph, once it loaded. */
  ops?: string[]
  /** Why in-process scoring is set but the worker still scores. */
  reason?: string
}

export async function getScoring(vhost: string, name: string, signal?: AbortSignal): Promise<ScoringStatus> {
  const res = await apiFetch(`${modelUrl(vhost, name)}/scoring`, { signal, silent: true })
  return (await res.json()) as ScoringStatus
}

export async function setScoring(vhost: string, name: string, scoring: Scoring): Promise<ScoringStatus> {
  const res = await apiFetch(`${modelUrl(vhost, name)}/scoring`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ scoring }),
    silent: true,
  })
  return (await res.json()) as ScoringStatus
}

/** The vhost's database sources, for reading a dataset from one. */
export async function listSQLSources(vhost: string, signal?: AbortSignal): Promise<Array<{ id: string; name: string; type: string }>> {
  const res = await apiFetch(`/api/sources?limit=500&vhost=${encodeURIComponent(vhost)}`, { signal, silent: true })
  const body = await res.json()
  const all = (body?.data ?? []) as Array<{ id: string; name: string; type: string }>
  return all.filter((s) => SQL_SOURCE_TYPES.includes(s.type))
}

/**
 * Where the training worker stands, for people: still being asked, not set up
 * (none connected), set up but not answering, or ready.
 */
export type WorkerState = 'checking' | 'off' | 'down' | 'ready'

export function workerState(status: WorkerStatus | undefined): WorkerState {
  if (!status) return 'checking'
  if (status.ready) return 'ready'
  return status.configured ? 'down' : 'off'
}

/** Why training cannot start yet, in one sentence; undefined when it can. */
export function trainBlockedReason(status: WorkerStatus | undefined): string | undefined {
  switch (workerState(status)) {
    case 'checking': return 'Checking whether the training worker is connected…'
    case 'off': return 'Training needs the hermod-ml worker, which is not set up yet.'
    case 'down': return 'Training needs the hermod-ml worker, which is set up but not answering.'
    default: return undefined
  }
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
// Custom training scripts: Python an Administrator saves per vhost, run in a
// sandbox on their own worker pool. Versioned by the SHA-256 of the source.
// ---------------------------------------------------------------------------

/** One version of a script (internal/storage MLScript). Listings leave out source. */
export interface MLScript {
  vhost: string
  name: string
  version: number
  sha256: string
  source?: string
  description?: string
  created_by?: string
  created_at: string
}

/** The worker refuses larger scripts (storage.MaxMLScriptBytes). */
export const MAX_SCRIPT_BYTES = 256 * 1024

export const SCRIPT_TEMPLATE = `import numpy as np


def train(df, spec):
    """Fit and return a model. df is a pandas DataFrame of spec["features"]
    and spec["target"]; spec["task"] is "classification" or "regression"."""
    raise NotImplementedError


def export_onnx(model, spec):
    """Return the model as ONNX bytes: one [n, 1] input per feature, named
    after it; a classifier outputs the label, then probabilities in
    spec["labels"] order."""
    raise NotImplementedError
`

export const scriptsKey = (vhost: string) => ['ml-scripts', vhost] as const
export const scriptKey = (vhost: string, name: string) => ['ml-script', vhost, name] as const

const scriptsUrl = (vhost: string) => `/api/vhosts/${encodeURIComponent(vhost)}/ml/scripts`
const scriptUrl = (vhost: string, name: string) => `${scriptsUrl(vhost)}/${encodeURIComponent(name)}`

export async function listScripts(vhost: string, signal?: AbortSignal): Promise<MLScript[]> {
  const res = await apiFetch(scriptsUrl(vhost), { signal, silent: true })
  const body = await res.json()
  return (body?.data ?? []) as MLScript[]
}

/** A script's latest version with its source, and every version, newest first. */
export async function getScript(vhost: string, name: string, signal?: AbortSignal): Promise<{ script: MLScript; versions: MLScript[] }> {
  const res = await apiFetch(scriptUrl(vhost, name), { signal, silent: true })
  const body = await res.json()
  return { script: body?.script as MLScript, versions: (body?.versions ?? []) as MLScript[] }
}

/** Saves source as the script's next version; the latest version's own source saves nothing new. */
export async function saveScript(vhost: string, name: string, source: string, description?: string): Promise<MLScript> {
  const res = await apiFetch(scriptUrl(vhost, name), {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ source, description: description ?? '' }),
    silent: true,
  })
  return (await res.json()) as MLScript
}

export async function deleteScript(vhost: string, name: string): Promise<void> {
  await apiFetch(scriptUrl(vhost, name), { method: 'DELETE', silent: true })
}

/** The vhost's scripts, for the algorithm pickers; empty unless the server has scripts on. */
export function useVHostScripts(vhost: string | undefined, enabled: boolean) {
  return useQuery({
    queryKey: scriptsKey(vhost ?? ''),
    queryFn: ({ signal }) => listScripts(vhost as string, signal).catch(() => [] as MLScript[]),
    enabled: enabled && isModelVHost(vhost),
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

export type CallerKind = 'workflow' | 'rest' | 'grpc' | 'mcp' | 'ui'

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
  /** A recommendation model's latest window, in place of drift (monitor.RecommendationReport). */
  recommendation?: RecommendationReport
}

/** How a recommendation model was used over one window. */
export interface RecommendationReport {
  vhost: string
  model: string
  version: string
  window_start: string
  window_end: string
  calls: number
  /** Calls answered with the popular items: an id the model did not know. */
  fallbacks: number
  popular_share: number
  /** Distinct items recommended, of the catalog's. */
  recommended: number
  catalog: number
  coverage: number
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
