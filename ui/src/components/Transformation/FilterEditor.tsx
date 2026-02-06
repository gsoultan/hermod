import { IconPlus, IconTrash } from '@tabler/icons-react';
import { Autocomplete, Button, Group, Select, Stack, Text, TextInput, ActionIcon } from '@mantine/core'export interface Condition {
  field: string
  operator: string
  value: string
}

interface FilterEditorProps {
  conditions: Condition[]
  availableFields: string[]
  onChange: (next: Condition[]) => void
}

export function FilterEditor({ conditions = [], availableFields = [], onChange }: FilterEditorProps) {
  const updateCondition = (index: number, field: keyof Condition, value: string) => {
    const next = [...conditions]
    next[index] = { ...next[index], [field]: value }
    onChange(next)
  }

  const removeCondition = (index: number) => {
    const next = conditions.filter((_, i) => i !== index)
    onChange(next)
  }

  const addCondition = () => {
    onChange([...conditions, { field: '', operator: '=', value: '' }])
  }

  return (
    <Stack gap="xs">
      <Text size="sm" fw={500}>
        Filter Conditions (AND)
      </Text>
      {conditions.map((cond, index) => (
        <Group
          key={index}
          grow
          gap="xs"
          align="flex-end"
          style={{ background: 'var(--mantine-color-gray-0)', padding: 8, borderRadius: 8 }}
        >
          <Stack gap={2}>
            <Text size="10px" c="dimmed">
              Field
            </Text>
            <Autocomplete
              placeholder="e.g. status"
              data={availableFields}
              size="xs"
              value={cond.field || ''}
              onChange={(val) => updateCondition(index, 'field', val)}
            />
          </Stack>
          <Stack gap={2}>
            <Text size="10px" c="dimmed">
              Operator
            </Text>
            <Select
              data={['=', '!=', '>', '>=', '<', '<=', 'contains', 'not_contains', 'regex', 'not_regex']}
              size="xs"
              value={cond.operator || '='}
              onChange={(val) => updateCondition(index, 'operator', val || '=')}
            />
          </Stack>
          <Stack gap={2}>
            <Text size="10px" c="dimmed">
              Value
            </Text>
            <TextInput
              placeholder="Value"
              size="xs"
              value={cond.value || ''}
              onChange={(e) => updateCondition(index, 'value', e.target.value)}
            />
          </Stack>
          <ActionIcon color="red" variant="subtle" onClick={() => removeCondition(index)} mb={2} aria-label="Remove condition">
            <IconTrash size="1rem" />
          </ActionIcon>
        </Group>
      ))}
      <Button size="xs" variant="light" leftSection={<IconPlus size="1rem" />} onClick={addCondition}>
        Add Condition
      </Button>
    </Stack>
  )
}


