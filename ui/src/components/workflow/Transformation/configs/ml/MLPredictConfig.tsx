import { useEffect, useMemo, useState } from 'react'
import { ActionIcon, Alert, Anchor, Autocomplete, Button, Checkbox, Group, Radio, Select, Stack, Text } from '@mantine/core'
import { TextInput } from '@/components/shared/ReferenceInputs'
import { IconInfoCircle, IconPlus, IconTrash } from '@tabler/icons-react'
import { useQuery } from '@tanstack/react-query'
import { isRecommender, listVersions, useVHostModels, versionsKey } from '@/lib/mlModels'
import { isScenarioVersion, type Scenario } from '@/lib/mlRecommend'
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
 * The model is chosen from the workflow vhost's registry (the Machine Learning page).
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

  // A recommendation model is only ever called in Recommend mode.
  const recommender = isRecommender(model)
  useEffect(() => {
    if (recommender && config.mode !== 'recommend') updateNodeConfig(nodeId, { mode: 'recommend' })
  }, [recommender, config.mode, nodeId, updateNodeConfig])
  const recommending = config.mode === 'recommend'
  // A recommender trained for a scenario is asked in that scenario's terms.
  const { data: versions } = useQuery({
    queryKey: versionsKey(vhost, model?.name ?? ''),
    queryFn: ({ signal }) => listVersions(vhost, model?.name as string, signal),
    enabled: recommender,
    retry: false,
    staleTime: 60_000,
  })
  const live = versions?.find((v) => v.version === model?.remote_version) ?? versions?.[0]
  const scenario = isScenarioVersion(live) ? live.scenario : undefined

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
          Register one on the <Anchor href="/ml/models">Machine Learning</Anchor> page: the address of a model
          server (hermod-ml, KServe, Triton, MLServer or MLflow) and the model's name on it.
        </Alert>
      )}
      {model?.description && <Text size="xs" c="dimmed">{model.description}</Text>}

      <Radio.Group label="Mode" value={recommending ? 'recommend' : 'predict'}
        onChange={(v) => set({ mode: v })}>
        <Group gap="lg" mt={4}>
          <Radio value="predict" label="Predict" disabled={recommender} />
          <Radio value="recommend" label="Recommend" />
        </Group>
      </Radio.Group>

      {recommending ? (
        scenario
          ? <ScenarioFields scenario={scenario} config={config} set={set} fieldPaths={fieldPaths} />
          : <RecommendFields config={config} set={set} fieldPaths={fieldPaths} />
      ) : (<>
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
      </>)}
    </Stack>
  )
}

/**
 * Recommend mode: the id of a person (or an item) is read from a record
 * field, and the model's items are written onto the record as a list of
 * {item, score, reason?}, best first.
 */
function RecommendFields({ config, set, fieldPaths }: {
  config: any; set: (patch: Record<string, unknown>) => void; fieldPaths: string[]
}) {
  const forItem = config.recommendFor === 'item'
  return (
    <Stack gap="sm">
      <Radio.Group label="Recommend" value={forItem ? 'item' : 'user'}
        onChange={(v) => set(v === 'item' ? { recommendFor: 'item', excludeSeen: undefined } : { recommendFor: 'user' })}>
        <Group gap="lg" mt={4}>
          <Radio value="user" label="A person's top items" />
          <Radio value="item" label="Items like an item" />
        </Group>
      </Radio.Group>
      <Autocomplete
        label="Field with the id"
        description={forItem ? 'The record field holding the item id.' : 'The record field holding the person id.'}
        placeholder={forItem ? 'e.g. product_id' : 'e.g. customer.id'}
        data={fieldPaths}
        value={config.idField ?? ''}
        onChange={(v) => set({ idField: v })}
        required
      />
      <TextInput
        label="How many"
        description="Items to return: 10 when empty, at most 100."
        placeholder="10"
        value={config.k ?? ''}
        onChange={(e) => set({ k: e.currentTarget.value })}
      />
      {!forItem && (
        <Checkbox
          label="Leave out what the person already had"
          checked={config.excludeSeen === undefined || config.excludeSeen === true || config.excludeSeen === 'true'}
          onChange={(e) => set({ excludeSeen: e.currentTarget.checked })}
        />
      )}
      <TextInput
        label="Output field"
        description={'Where the items are written: a list of {item, score, reason}. Reason "popular" marks the fallback for an id the model never saw.'}
        placeholder="recommendations"
        value={config.outputField ?? ''}
        onChange={(e) => set({ outputField: e.currentTarget.value })}
      />
    </Stack>
  )
}

/** Who a scenario is asked for: a salesman's plan, a customer's list, or a product's. */
const SCENARIO_FOR: Record<Scenario, 'salesman' | 'customer' | 'product'> = {
  visits: 'salesman', products: 'customer', reorder: 'customer', audience: 'product', similar: 'product',
}

/**
 * Recommend mode for a scenario model: the id it is asked for (a salesman, a
 * customer or a product, as the scenario decides) is read from a record field,
 * a visit plan covers the next days from when the record arrives, and the
 * answer, with its reasons, is written onto the record.
 */
function ScenarioFields({ scenario, config, set, fieldPaths }: {
  scenario: Scenario; config: any; set: (patch: Record<string, unknown>) => void; fieldPaths: string[]
}) {
  const forKey = SCENARIO_FOR[scenario]
  useEffect(() => {
    if (config.recommendFor !== forKey) set(forKey === 'customer' ? { recommendFor: forKey } : { recommendFor: forKey, excludeSeen: undefined })
  }, [config.recommendFor, forKey, set])
  return (
    <Stack gap="sm">
      <Autocomplete
        label={`Field with the ${forKey} id`}
        description={`The record field holding the ${forKey} id, as in the training data.`}
        placeholder={forKey === 'salesman' ? 'e.g. rep_id' : forKey === 'customer' ? 'e.g. customer.id' : 'e.g. sku'}
        data={fieldPaths}
        value={config.idField ?? ''}
        onChange={(v) => set({ idField: v })}
        required
      />
      {scenario === 'visits' && (
        <TextInput
          label="Days to plan"
          description="The plan covers this many days from the day after the record arrives: 7 when empty."
          placeholder="7"
          value={config.periodDays ?? ''}
          onChange={(e) => set({ periodDays: e.currentTarget.value })}
        />
      )}
      <TextInput
        label="How many"
        description={scenario === 'visits' ? 'Visits in the plan: 10 when empty, at most 500.' : 'Items to return: 10 when empty, at most 100.'}
        placeholder="10"
        value={config.k ?? ''}
        onChange={(e) => set({ k: e.currentTarget.value })}
      />
      {scenario === 'products' && (
        <Checkbox
          label="Leave out what the customer already bought"
          checked={config.excludeSeen === undefined || config.excludeSeen === true || config.excludeSeen === 'true'}
          onChange={(e) => set({ excludeSeen: e.currentTarget.checked })}
        />
      )}
      <TextInput
        label="Output field"
        description={scenario === 'visits'
          ? 'Where the plan is written: a list of days, each with its customers and reasons.'
          : 'Where the answer is written: a list of {id, score, rank, reasons}.'}
        placeholder="recommendations"
        value={config.outputField ?? ''}
        onChange={(e) => set({ outputField: e.currentTarget.value })}
      />
    </Stack>
  )
}
