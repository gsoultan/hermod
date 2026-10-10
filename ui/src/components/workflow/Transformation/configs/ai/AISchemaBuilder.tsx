import { useState } from 'react'
import { ActionIcon, Box, Button, Checkbox, Group, Select, Stack, Text, TextInput } from '@mantine/core'
import { IconPlus, IconTrash } from '@tabler/icons-react'

type FieldType = 'string' | 'number' | 'integer' | 'boolean' | 'string[]'

interface FieldRow {
  name: string
  type: FieldType
  description: string
  required: boolean
}

const TYPES: { value: FieldType; label: string }[] = [
  { value: 'string', label: 'Text' },
  { value: 'number', label: 'Number' },
  { value: 'integer', label: 'Whole number' },
  { value: 'boolean', label: 'Yes / no' },
  { value: 'string[]', label: 'List of text' },
]

/** The JSON Schema for a list of fields, as the text the node stores. */
export function schemaFromFields(rows: FieldRow[]): string {
  const properties: Record<string, unknown> = {}
  const required: string[] = []
  for (const row of rows) {
    const name = row.name.trim()
    if (!name) continue
    const prop: Record<string, unknown> =
      row.type === 'string[]' ? { type: 'array', items: { type: 'string' } } : { type: row.type }
    if (row.description.trim()) prop.description = row.description.trim()
    properties[name] = prop
    if (row.required) required.push(name)
  }
  return JSON.stringify({ type: 'object', properties, required }, null, 2)
}

interface AISchemaBuilderProps {
  /** Whether applying replaces a schema that is already there. */
  replaces: boolean
  onApply: (schema: string) => void
}

/** A small form that writes the extract schema for people who do not write JSON Schema. */
export function AISchemaBuilder({ replaces, onApply }: AISchemaBuilderProps) {
  const [rows, setRows] = useState<FieldRow[]>([])
  const update = (i: number, patch: Partial<FieldRow>) =>
    setRows((rs) => rs.map((r, j) => (j === i ? { ...r, ...patch } : r)))
  const named = rows.some((r) => r.name.trim() !== '')

  return (
    <Box p="sm" style={{ border: '1px solid var(--mantine-color-default-border)', borderRadius: 'var(--mantine-radius-sm)' }}>
      <Stack gap="xs">
        {rows.length === 0 && (
          <Text size="sm" c="dimmed">
            No fields yet. Add one for each value the model should pull out of the record.
          </Text>
        )}
        {rows.map((row, i) => (
          <Group key={i} gap="xs" align="flex-end" wrap="nowrap">
            <TextInput
              aria-label={`Field ${i + 1} name`}
              placeholder="Name, e.g. invoice_no"
              value={row.name}
              onChange={(e) => update(i, { name: e.currentTarget.value })}
              flex={2}
              size="xs"
            />
            <Select
              aria-label={`Field ${i + 1} type`}
              data={TYPES}
              value={row.type}
              onChange={(v) => update(i, { type: (v as FieldType) || 'string' })}
              allowDeselect={false}
              flex={1}
              size="xs"
            />
            <TextInput
              aria-label={`Field ${i + 1} description`}
              placeholder="What it means (optional)"
              value={row.description}
              onChange={(e) => update(i, { description: e.currentTarget.value })}
              flex={2}
              size="xs"
            />
            <Checkbox
              label="Required"
              checked={row.required}
              onChange={(e) => update(i, { required: e.currentTarget.checked })}
              size="xs"
              mb={6}
            />
            <ActionIcon
              aria-label={`Remove field ${i + 1}`}
              color="red"
              variant="subtle"
              onClick={() => setRows((rs) => rs.filter((_, j) => j !== i))}
              mb={2}
            >
              <IconTrash size="1rem" />
            </ActionIcon>
          </Group>
        ))}
        <Group justify="space-between">
          <Button
            size="xs"
            variant="light"
            leftSection={<IconPlus size="0.9rem" />}
            onClick={() => setRows((rs) => [...rs, { name: '', type: 'string', description: '', required: true }])}
          >
            Add field
          </Button>
          <Button size="xs" disabled={!named} onClick={() => onApply(schemaFromFields(rows))}>
            {replaces ? 'Use these fields (replaces the schema)' : 'Use these fields'}
          </Button>
        </Group>
      </Stack>
    </Box>
  )
}
