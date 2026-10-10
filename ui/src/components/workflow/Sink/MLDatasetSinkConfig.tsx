import { ActionIcon, Alert, Autocomplete, Button, Group, NumberInput, Select, Stack, Text, TextInput } from '@mantine/core'
import { IconInfoCircle, IconPlus, IconTrash } from '@tabler/icons-react'
import { DATASET_NAME_PATTERN, useVHostDatasets } from '@/lib/mlModels'

interface ColumnMapping {
  source_field: string
  target_column: string
}

/** The masks the sink applies, as the Mask node applies them. */
const MASK_TYPES = [
  { value: 'all', label: 'Replace entirely (****)' },
  { value: 'partial', label: 'Keep the first and last characters' },
  { value: 'email', label: 'Keep the email domain' },
  { value: 'pii', label: 'Mask emails, cards and phone numbers in text' },
]

export interface MLDatasetSinkConfigProps {
  config: Record<string, string>
  updateConfig: (key: string, value: string) => void
  /** The sink's vhost, whose datasets are offered. */
  vhost?: string
}

/**
 * Collect Dataset: every record the workflow sends here becomes a row of a
 * dataset of the workflow's vhost, on Hermod's ML worker, for Train Model or a
 * retrain policy to learn from. Rows are appended; nothing is replaced.
 */
export function MLDatasetSinkConfig({ config, updateConfig, vhost }: MLDatasetSinkConfigProps) {
  const { data: datasets = [] } = useVHostDatasets(vhost)
  const dataset = config.dataset || ''
  const badName = dataset !== '' && !DATASET_NAME_PATTERN.test(dataset)

  let mappings: ColumnMapping[] = []
  try {
    mappings = config.column_mappings ? (JSON.parse(config.column_mappings) as ColumnMapping[]) : []
  } catch {
    mappings = []
  }
  const setMappings = (next: ColumnMapping[]) => updateConfig('column_mappings', next.length ? JSON.stringify(next) : '')
  const setMapping = (i: number, patch: Partial<ColumnMapping>) =>
    setMappings(mappings.map((m, j) => (j === i ? { ...m, ...patch } : m)))

  return (
    <Stack gap="md">
      <Autocomplete
        label="Dataset"
        description="A dataset of this vhost. A new name starts a new dataset."
        placeholder="customers"
        required
        data={datasets.map((d) => d.name)}
        value={dataset}
        onChange={(v) => updateConfig('dataset', v)}
        error={badName ? 'Letters, digits, "_", "." and "-"; starting with a letter or digit.' : undefined}
      />

      <Stack gap={6}>
        <Text size="sm" fw={500}>Columns</Text>
        <Text size="xs" c="dimmed">
          Which fields become which columns. With none, the whole record is a row, nested fields as parent_child columns.
        </Text>
        {mappings.map((m, i) => (
          <Group key={i} gap="xs" align="flex-end" wrap="nowrap">
            <TextInput aria-label={`Field ${i + 1}`} placeholder="customer.age" style={{ flex: 1 }} value={m.source_field}
              onChange={(e) => setMapping(i, { source_field: e.currentTarget.value })} />
            <TextInput aria-label={`Column ${i + 1}`} placeholder="age" style={{ flex: 1 }} value={m.target_column}
              onChange={(e) => setMapping(i, { target_column: e.currentTarget.value })} />
            <ActionIcon variant="subtle" color="red" aria-label={`Remove column ${i + 1}`}
              onClick={() => setMappings(mappings.filter((_, j) => j !== i))}>
              <IconTrash size="1rem" />
            </ActionIcon>
          </Group>
        ))}
        <Group>
          <Button variant="light" size="xs" leftSection={<IconPlus size="0.9rem" />}
            onClick={() => setMappings([...mappings, { source_field: '', target_column: '' }])}>
            Add column
          </Button>
        </Group>
      </Stack>

      <Group grow align="flex-start">
        <TextInput label="Mask these columns" description="Comma separated; masked before the row leaves Hermod."
          placeholder="email, phone" value={config.mask_fields || ''} onChange={(e) => updateConfig('mask_fields', e.currentTarget.value)} />
        <Select label="Mask" data={MASK_TYPES} value={config.mask_type || 'all'} allowDeselect={false}
          onChange={(v) => updateConfig('mask_type', v ?? 'all')} />
      </Group>

      <NumberInput label="Stop at this many rows" description="Later records are not added once the dataset holds this many."
        placeholder="1000000" min={1} allowDecimal={false} value={config.max_rows ? Number(config.max_rows) : ''}
        onChange={(n) => updateConfig('max_rows', typeof n === 'number' && n > 0 ? String(n) : '')} />

      <Alert variant="light" color="blue" icon={<IconInfoCircle size="1rem" />}>
        Rows go to the ML worker in batches: 500 at a time, or every 5 seconds, unless the reliability step says
        otherwise. A failed batch is retried and then dead-lettered like any other sink&apos;s.
      </Alert>
    </Stack>
  )
}
