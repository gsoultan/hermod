import { useMemo, useState } from 'react'
import { ActionIcon, Alert, Anchor, Autocomplete, Button, Group, Select, Stack, Text, TextInput } from '@mantine/core'
import { IconInfoCircle, IconPlus, IconTrash } from '@tabler/icons-react'
import { useVHostModels } from '@/lib/mlModels'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'

interface MLPredictConfigProps {
  config: any
  updateNodeConfig: (id: string, config: any) => void
  nodeId: string
  fieldPaths?: string[]
}

type Row = { id: string; feature: string; field: string }

// Row ids key rows, never names, so a row being renamed keeps its input
// mounted. A row offered for a declared feature is keyed by that feature, and
// keeps the key once something is typed into it.
let lastRowId = 0
const newRowId = () => `r${++lastRowId}`
const featureRowId = (feature: string) => `f:${feature}`

/** inputs is saved as JSON object text of feature -> field path. */
function rowsFrom(text: unknown): Array<Omit<Row, 'id'>> {
  if (typeof text !== 'string' || text.trim() === '') return []
  try {
    const obj = JSON.parse(text)
    if (!obj || typeof obj !== 'object' || Array.isArray(obj)) return []
    return Object.entries(obj).map(([feature, field]) => ({ feature, field: String(field ?? '') }))
  } catch {
    return []
  }
}

function textFrom(rows: Row[]): string {
  const obj: Record<string, string> = {}
  for (const r of rows) {
    if (r.feature.trim()) obj[r.feature.trim()] = r.field.trim()
  }
  return Object.keys(obj).length ? JSON.stringify(obj) : ''
}

/**
 * The Predict node: run each record through one of the vhost's models.
 *
 * The model is chosen from the workflow vhost's registry (the Models page).
 * Features are mapped to record fields; with no mapping the whole record is
 * sent. The answer lands in one field: the value itself for a model with one
 * output, an object for a model with several.
 */
export function MLPredictConfig({ config, updateNodeConfig, nodeId, fieldPaths = [] }: MLPredictConfigProps) {
  const vhost = useWorkflowStore((s) => s.vhost) || 'default'
  const { data: models = [], isLoading } = useVHostModels(vhost)
  const set = (patch: Record<string, unknown>) => updateNodeConfig(nodeId, patch)

  const [rows, setRows] = useState<Row[]>(() => rowsFrom(config.inputs).map((r) => ({ ...r, id: newRowId() })))
  const commit = (next: Row[]) => {
    setRows(next)
    set({ inputs: textFrom(next) })
  }

  const model = useMemo(() => models.find((m) => m.name === config.model), [models, config.model])

  // A model that declares its features is offered a row for each one the
  // mapping lacks, so the person fills in fields rather than retyping feature
  // names. Those rows are derived, not stored, until something is typed.
  const shown = useMemo(() => {
    const have = new Set(rows.map((r) => r.feature))
    const offered = (model?.features ?? [])
      .filter((f) => !have.has(f))
      .map((feature) => ({ id: featureRowId(feature), feature, field: '' }))
    return [...rows, ...offered]
  }, [rows, model])

  // Editing a row stores it; offered rows nobody touched stay derived.
  const update = (id: string, patch: Partial<Row>) => {
    const stored = new Set(rows.map((r) => r.id))
    commit(
      shown
        .filter((r) => stored.has(r.id) || r.id === id)
        .map((r) => (r.id === id ? { ...r, ...patch } : r)),
    )
  }

  return (
    <Stack gap="sm">
      <Select
        label="Model"
        description={`A model registered in ${vhost}.`}
        placeholder={isLoading ? 'Loading models…' : 'Choose a model'}
        data={models.map((m) => ({ value: m.name, label: m.name }))}
        value={config.model || null}
        onChange={(v) => set({ model: v ?? '' })}
        searchable
        required
        nothingFoundMessage="No model by that name"
      />
      {!isLoading && models.length === 0 && (
        <Alert color="blue" icon={<IconInfoCircle size="1rem" />} title={`No models in ${vhost} yet`}>
          Register one on the <Anchor href="/ml/models">Models</Anchor> page: the address of a model
          server (hermod-ml, KServe, Triton, MLServer or MLflow) and the model's name on it.
        </Alert>
      )}
      {model?.description && <Text size="xs" c="dimmed">{model.description}</Text>}

      <Stack gap={6}>
        <Text size="sm" fw={500}>Inputs</Text>
        <Text size="xs" c="dimmed">
          Which record field feeds each model feature. Leave empty to send the whole record.
        </Text>
        {shown.map((r) => (
          <Group key={r.id} gap="xs" wrap="nowrap" align="flex-end">
            <TextInput
              aria-label="Feature"
              placeholder="feature"
              value={r.feature}
              onChange={(e) => update(r.id, { feature: e.currentTarget.value })}
              style={{ flex: 1 }}
            />
            <Autocomplete
              aria-label={`Record field for ${r.feature || 'feature'}`}
              placeholder="record field, e.g. order.total"
              data={fieldPaths}
              value={r.field}
              onChange={(v) => update(r.id, { field: v })}
              style={{ flex: 1 }}
            />
            <ActionIcon variant="subtle" color="red" aria-label={`Remove ${r.feature || 'row'}`}
              onClick={() => commit(rows.filter((x) => x.id !== r.id))}>
              <IconTrash size="1rem" />
            </ActionIcon>
          </Group>
        ))}
        <Group>
          <Button size="xs" variant="light" leftSection={<IconPlus size="0.9rem" />}
            onClick={() => setRows([...rows, { id: newRowId(), feature: '', field: '' }])}>
            Add input
          </Button>
        </Group>
      </Stack>

      <TextInput
        label="Output field"
        description="Where the prediction is written on the record."
        placeholder="prediction"
        value={config.outputField ?? ''}
        onChange={(e) => set({ outputField: e.currentTarget.value })}
      />
    </Stack>
  )
}
