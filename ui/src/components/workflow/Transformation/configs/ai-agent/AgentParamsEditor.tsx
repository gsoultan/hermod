import { ActionIcon, Button, Checkbox, Group, Select, Stack, Text, TextInput } from '@mantine/core'
import { IconPlus, IconTrash } from '@tabler/icons-react'
import { PARAM_TYPES, type AgentParam } from './agentTools'

interface AgentParamsEditorProps {
  params: AgentParam[]
  onChange: (params: AgentParam[]) => void
}

const TYPE_OPTIONS = PARAM_TYPES.map((t) => ({ value: t, label: t }))

/**
 * The arguments the model may fill in for a tool (config.go `parameters`).
 * Anything else the model sends is dropped before the tool runs, so these are
 * the only values a call can carry.
 */
export function AgentParamsEditor({ params, onChange }: AgentParamsEditorProps) {
  const update = (i: number, patch: Partial<AgentParam>) =>
    onChange(params.map((p, j) => (j === i ? { ...p, ...patch } : p)))
  const add = () => onChange([...params, { name: '', type: 'string', description: '', required: false }])
  const remove = (i: number) => onChange(params.filter((_, j) => j !== i))

  return (
    <Stack gap="xs">
      <Text size="sm" fw={500}>
        Arguments the model fills in
      </Text>
      <Text size="xs" c="dimmed">
        The tool receives only these, as fields of the record it works on. Leave empty for a tool that takes none.
      </Text>
      {params.map((p, i) => (
        <Group key={i} gap="xs" align="flex-end" wrap="nowrap">
          <TextInput
            label={`Argument ${i + 1} name`}
            placeholder="e.g. email"
            value={p.name ?? ''}
            onChange={(e) => update(i, { name: e.currentTarget.value })}
            size="xs"
            flex={2}
          />
          <Select
            label={`Argument ${i + 1} type`}
            data={TYPE_OPTIONS}
            value={p.type || 'string'}
            onChange={(v) => update(i, { type: v ?? 'string' })}
            allowDeselect={false}
            size="xs"
            flex={1}
          />
          <TextInput
            label={`Argument ${i + 1} description`}
            placeholder="What to pass"
            value={p.description ?? ''}
            onChange={(e) => update(i, { description: e.currentTarget.value })}
            size="xs"
            flex={3}
          />
          <Checkbox
            aria-label={`Argument ${i + 1} required`}
            label="Required"
            checked={p.required === true}
            onChange={(e) => update(i, { required: e.currentTarget.checked })}
            size="xs"
            mb={6}
          />
          <ActionIcon aria-label={`Remove argument ${i + 1}`} color="red" variant="subtle" onClick={() => remove(i)} mb={2}>
            <IconTrash size="1rem" />
          </ActionIcon>
        </Group>
      ))}
      <Button size="xs" variant="subtle" leftSection={<IconPlus size="0.9rem" />} onClick={add} w="fit-content">
        Add argument
      </Button>
    </Stack>
  )
}
