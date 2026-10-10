import { Alert, Autocomplete, Input, SegmentedControl, Stack, Switch, Text, TextInput } from '@mantine/core'
import { IconInfoCircle } from '@tabler/icons-react'

interface ExplodeConfigProps {
  config: any
  updateNodeConfig: (id: string, config: any) => void
  nodeId: string
  fieldPaths?: string[]
}

/**
 * The explode node (internal/engine/registry/nodes/control/explode.go): one
 * record per array element, the record's other fields kept and the array
 * dropped. Saves arrayPath, mode ("field" or "merge"), targetField,
 * indexField, maxItems and keepEmpty.
 */
export function ExplodeConfig({ config, updateNodeConfig, nodeId, fieldPaths = [] }: ExplodeConfigProps) {
  const set = (patch: Record<string, unknown>) => updateNodeConfig(nodeId, patch)
  const merge = config.mode === 'merge'
  const missingPath = !String(config.arrayPath ?? '').trim()

  return (
    <Stack gap="sm">
      {missingPath && (
        <Alert icon={<IconInfoCircle size="1rem" />} color="red">
          <Text size="sm">Array Path is required: it names the list each record is split by.</Text>
        </Alert>
      )}
      <Text size="sm" c="dimmed">
        Emits one record per element of the array, with the record's other fields on each. Downstream nodes run
        once per element; a Collect node can regroup them.
      </Text>
      <Autocomplete
        label="Array path"
        placeholder="e.g. lines, order.items"
        data={fieldPaths}
        value={config.arrayPath ?? ''}
        onChange={(v) => set({ arrayPath: v })}
        required
      />
      <Input.Wrapper label="Each element">
        <SegmentedControl
          fullWidth
          mt={4}
          value={merge ? 'merge' : 'field'}
          onChange={(v) => set({ mode: v })}
          data={[
            { value: 'field', label: 'Into a field' },
            { value: 'merge', label: 'Merge into the record' },
          ]}
        />
      </Input.Wrapper>
      {merge ? (
        <Text size="xs" c="dimmed">
          An element must be an object; its fields are written onto the record and win over a field of the same name.
        </Text>
      ) : (
        <TextInput
          label="Target field"
          placeholder={config.arrayPath || 'the array path'}
          description="Where the element goes; defaults to the array's own path"
          value={config.targetField ?? ''}
          onChange={(e) => set({ targetField: e.currentTarget.value })}
        />
      )}
      <TextInput
        label="Index field"
        placeholder="e.g. line_no"
        description="Optional: receives the element's position, from 0"
        value={config.indexField ?? ''}
        onChange={(e) => set({ indexField: e.currentTarget.value })}
      />
      <TextInput
        label="Max items"
        placeholder="10000"
        description="The most records one record may become. A longer array fails the record instead of emitting part of it."
        inputMode="numeric"
        value={config.maxItems ?? ''}
        onChange={(e) => set({ maxItems: e.currentTarget.value.replace(/\D/g, '') })}
      />
      <Switch
        label="Pass the record on when the array is empty"
        description="Off: a record with an empty array emits nothing"
        checked={config.keepEmpty === true || config.keepEmpty === 'true'}
        onChange={(e) => set({ keepEmpty: e.currentTarget.checked })}
      />
    </Stack>
  )
}
