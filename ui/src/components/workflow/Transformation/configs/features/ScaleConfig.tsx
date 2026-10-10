import { useState } from 'react'
import { ActionIcon, Autocomplete, Button, Group, JsonInput, NumberInput, SegmentedControl, Stack, Switch, Text, TextInput } from '@mantine/core'
import { IconPlus, IconTrash } from '@tabler/icons-react'
import { type FeatureConfigProps, OnMissingSelect, num } from './shared'

type Row = { id: string; field: string; min?: number; max?: number; mean?: number; std?: number; targetField?: string }

// Row ids key rows, never field names, so renaming a field keeps its inputs
// mounted.
let lastRowId = 0
const newRowId = () => `s${++lastRowId}`

function rowsFrom(fields: unknown): Row[] {
  let list = fields
  if (typeof list === 'string') {
    try {
      list = JSON.parse(list)
    } catch {
      return []
    }
  }
  if (!Array.isArray(list)) return []
  return list
    .filter((r) => r && typeof r === 'object')
    .map((r: any) => ({ ...r, field: String(r.field ?? ''), id: newRowId() }))
}

/** What is saved: each row without its id, and without blank entries. */
function savedRows(rows: Row[]) {
  return rows.map(({ id: _id, ...r }) =>
    Object.fromEntries(Object.entries(r).filter(([, v]) => v !== undefined && v !== '')),
  )
}

/**
 * Scale: rescale numeric fields with statistics fitted at training time. The
 * numbers are typed or pasted once and never refitted on live records, so a
 * model is served the scale it was trained on.
 */
export function ScaleConfig({ config, updateNodeConfig, nodeId, fieldPaths = [] }: FeatureConfigProps) {
  const set = (patch: Record<string, unknown>) => updateNodeConfig(nodeId, patch)
  const method = config.method === 'zscore' ? 'zscore' : 'minmax'
  const [rows, setRows] = useState<Row[]>(() => rowsFrom(config.fields))
  const commit = (next: Row[]) => {
    setRows(next)
    set({ fields: savedRows(next) })
  }
  const update = (id: string, patch: Partial<Row>) => commit(rows.map((r) => (r.id === id ? { ...r, ...patch } : r)))

  const [a, b] = method === 'zscore' ? (['mean', 'std'] as const) : (['min', 'max'] as const)
  const statLabel = { min: 'Min', max: 'Max', mean: 'Mean', std: 'Std' }

  return (
    <Stack gap="sm">
      <Stack gap={4}>
        <Text size="sm" fw={500}>Method</Text>
        <SegmentedControl
          data={[
            { label: 'Min-max', value: 'minmax' },
            { label: 'Z-score', value: 'zscore' },
          ]}
          value={method}
          onChange={(v) => set({ method: v })}
        />
        <Text size="xs" c="dimmed">
          {method === 'zscore' ? '(value − mean) / std' : '(value − min) / (max − min)'}, with the statistics
          of the training data.
        </Text>
      </Stack>

      <Stack gap={6}>
        <Text size="sm" fw={500}>Fields</Text>
        {rows.map((r) => {
          const name = r.field || 'field'
          return (
            <Group key={r.id} gap="xs" wrap="nowrap" align="flex-end">
              <Autocomplete
                aria-label="Field to scale"
                placeholder="field"
                data={fieldPaths}
                value={r.field}
                onChange={(v) => update(r.id, { field: v })}
                style={{ flex: 2 }}
              />
              <NumberInput
                aria-label={`${statLabel[a]} of ${name}`}
                placeholder={a}
                value={r[a] ?? ''}
                onChange={(v) => update(r.id, { [a]: num(v) })}
                style={{ flex: 1 }}
              />
              <NumberInput
                aria-label={`${statLabel[b]} of ${name}`}
                placeholder={b}
                value={r[b] ?? ''}
                onChange={(v) => update(r.id, { [b]: num(v) })}
                style={{ flex: 1 }}
              />
              <TextInput
                aria-label={`Target field for ${name}`}
                placeholder={r.field ? `${r.field}_scaled` : 'target'}
                value={r.targetField ?? ''}
                onChange={(e) => update(r.id, { targetField: e.currentTarget.value })}
                style={{ flex: 2 }}
              />
              <ActionIcon variant="subtle" color="red" aria-label={`Remove ${name}`}
                onClick={() => commit(rows.filter((x) => x.id !== r.id))}>
                <IconTrash size="1rem" />
              </ActionIcon>
            </Group>
          )
        })}
        <Group>
          <Button size="xs" variant="light" leftSection={<IconPlus size="0.9rem" />}
            onClick={() => setRows([...rows, { id: newRowId(), field: '' }])}>
            Add field
          </Button>
        </Group>
        <Text size="xs" c="dimmed">
          A field with blank statistics takes them from the pasted stats below.
        </Text>
      </Stack>

      <JsonInput
        label="Fitted stats (JSON)"
        description='Paste from training: {"amount": {"mean": 120.5, "std": 40.2}}, or min and max. With no fields above, every field here is scaled.'
        placeholder='{"amount": {"min": 0, "max": 500}}'
        value={typeof config.stats === 'string' ? config.stats : config.stats ? JSON.stringify(config.stats) : ''}
        onChange={(v) => set({ stats: v })}
        validationError="Not valid JSON"
        autosize
        minRows={3}
      />

      {method === 'minmax' && (
        <Switch
          label="Clip to [0, 1]"
          description="A value outside the training range scales past 0 or 1 unless clipped."
          checked={config.clip === true || config.clip === 'true'}
          onChange={(e) => set({ clip: e.currentTarget.checked })}
        />
      )}
      <OnMissingSelect value={config.onMissing} onChange={(v) => set({ onMissing: v })} />
    </Stack>
  )
}
