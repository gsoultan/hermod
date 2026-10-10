import { Alert, Divider, Group, MultiSelect, Select, Stack, Text, TextInput, Textarea } from '@mantine/core'
import { IconInfoCircle } from '@tabler/icons-react'
import {
  SQL_SOURCE_TYPES, TASK_OPTIONS, algorithmOptions, isDeepAlgorithm, useDataset, useVHostDatasets, useWorkerStatus,
  type GoLive,
} from '@/lib/mlModels'
import { DeepParamsFields, readDeepParams } from '@/pages/ml/deepParams'
import { GoLiveField } from '@/pages/ml/goLive'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'

interface MLTrainConfigProps {
  config: any
  updateNodeConfig: (id: string, config: any) => void
  nodeId: string
  sources?: any[]
}

/** features is saved as JSON list text; older or hand-written configs may be comma-separated. */
function featuresFrom(v: unknown): string[] {
  if (Array.isArray(v)) return v.map(String)
  if (typeof v !== 'string' || !v.trim()) return []
  if (v.trim().startsWith('[')) {
    try {
      const list = JSON.parse(v)
      return Array.isArray(list) ? list.map(String) : []
    } catch {
      return []
    }
  }
  return v.split(',').map((s) => s.trim()).filter(Boolean)
}

const num = (v: unknown) => (v === undefined || v === '' ? undefined : Number(v))

/**
 * The Train Model node: each message that reaches it trains a new version of a
 * model on a dataset, optionally refilled from a database first, and writes
 * the version, its metrics and whether it went live onto the message. The
 * saved keys are what pkg/comm/transformer/ml reads.
 */
export function MLTrainConfig({ config, updateNodeConfig, nodeId, sources = [] }: MLTrainConfigProps) {
  const vhost = useWorkflowStore((s) => s.vhost) || 'default'
  const set = (patch: Record<string, unknown>) => updateNodeConfig(nodeId, patch)
  const { data: worker } = useWorkerStatus()
  const { data: datasets = [] } = useVHostDatasets(vhost)
  const { data: info } = useDataset(vhost, config.dataset)
  const columns = (info?.columns ?? []).map((c) => c.name)
  const features = featuresFrom(config.features)
  const dbSources = (Array.isArray(sources) ? sources : [])
    .filter((s: any) => SQL_SOURCE_TYPES.includes(s.type) && (s.vhost || 'default') === vhost)
    .map((s: any) => ({ value: s.id, label: s.name }))

  // Saved as typed; the engine reads them (pkg/comm/transformer/ml deepParams).
  const deepText = {
    hiddenLayers: config.hiddenLayers ?? '', epochs: config.epochs ?? '', batchSize: config.batchSize ?? '',
    learningRate: config.learningRate ?? '', patience: config.patience ?? '',
  }

  const goLive: GoLive = {
    mode: (config.goLive || 'never') as GoLive['mode'],
    metric: config.goLiveMetric || undefined,
    min: num(config.goLiveMin),
  }
  const setGoLive = (g: GoLive) =>
    set({
      goLive: g.mode,
      goLiveMetric: g.mode === 'if' ? g.metric ?? 'score' : '',
      goLiveMin: g.mode === 'if' && g.min !== undefined ? String(g.min) : '',
    })

  return (
    <Stack gap="sm">
      {worker && !worker.ready && (
        <Alert color="yellow" variant="light" icon={<IconInfoCircle size="1rem" />}>
          Training needs the hermod-ml worker, which {worker.configured ? 'does not answer' : 'is not configured (HERMOD_ML_WORKER_URL)'}.
          This node fails until it does.
        </Alert>
      )}
      <Text size="xs" c="dimmed">
        Every message that reaches this node trains a new version, so put it in a workflow that runs on a schedule or
        on demand, not one that sees every change to a table.
      </Text>
      <TextInput label="Model name" required placeholder="churn" description="A new name creates the model; an existing one gets a new version."
        value={config.model ?? ''} onChange={(e) => set({ model: e.currentTarget.value })} />
      <Select label="Dataset" required searchable placeholder={datasets.length ? 'Choose a dataset' : 'No datasets yet (see Models)'}
        data={datasets.map((d) => ({ value: d.name, label: d.name }))} value={config.dataset || null}
        onChange={(v) => set({ dataset: v ?? '', target: '', features: '' })} />
      <Select label="Column to predict" required searchable disabled={!config.dataset} data={columns} value={config.target || null}
        onChange={(v) => set({ target: v ?? '', features: features.length ? JSON.stringify(features.filter((f) => f !== v)) : '' })} />
      <MultiSelect label="Learn from" description="Empty learns from every other column." searchable clearable disabled={!config.target}
        data={columns.filter((c) => c !== config.target)} value={features}
        onChange={(list) => set({ features: list.length ? JSON.stringify(list) : '' })} />
      <Group grow align="flex-start">
        <Select label="Task" data={TASK_OPTIONS} value={config.task || 'auto'} allowDeselect={false} onChange={(v) => set({ task: v ?? 'auto' })} />
        <Select label="Algorithm" data={algorithmOptions(worker)} value={config.algorithm || 'auto'} allowDeselect={false}
          onChange={(v) => set({ algorithm: v ?? 'auto' })} />
      </Group>
      {isDeepAlgorithm(config.algorithm) && (
        <DeepParamsFields value={deepText} errors={readDeepParams(deepText).errors} onChange={(patch) => set({ ...patch })} />
      )}
      <GoLiveField value={goLive} onChange={setGoLive} />

      <Divider label="Refill from a database first (optional)" labelPosition="left" mt="xs" />
      <Select label="Database source" searchable clearable placeholder={dbSources.length ? 'None: train on the dataset as it is' : 'No database sources in this vhost'}
        data={dbSources} value={config.sourceId || null} onChange={(v) => set({ sourceId: v ?? '' })} />
      {config.sourceId && (
        <>
          <Textarea label="Query" autosize minRows={3} placeholder="SELECT age, plan, churned FROM customers"
            description="A SELECT; its rows replace the dataset before every training." value={config.query ?? ''}
            onChange={(e) => set({ query: e.currentTarget.value })} styles={{ input: { fontFamily: 'var(--mantine-font-family-monospace)' } }} />
          <TextInput label="At most this many rows" placeholder="1000000" value={config.maxRows ?? ''} maw={260}
            onChange={(e) => set({ maxRows: e.currentTarget.value })} />
        </>
      )}

      <TextInput label="Output field" placeholder="training" description="Gets the version, its metrics and whether it went live."
        value={config.outputField ?? ''} onChange={(e) => set({ outputField: e.currentTarget.value })} />
    </Stack>
  )
}
