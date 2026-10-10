import { Autocomplete, Group, Input, SegmentedControl, Stack, Text, TextInput } from '@mantine/core'

interface FlattenConfigProps {
  config: any
  updateNodeConfig: (id: string, config: any) => void
  nodeId: string
  fieldPaths?: string[]
  /** flatten or unflatten: one editor serves both, as their settings undo each other. */
  transType?: string
}

/**
 * Flatten turns {"a":{"b":1}} into {"a_b":1}; Unflatten turns it back. Both
 * read the settings pkg/comm/transformer/structure/flatten.go reads:
 * separator, maxDepth, arrays ("index" or "keep"), field and targetField.
 */
export function FlattenConfig({ config, updateNodeConfig, nodeId, fieldPaths = [], transType }: FlattenConfigProps) {
  const unflatten = transType === 'unflatten'
  const set = (patch: Record<string, unknown>) => updateNodeConfig(nodeId, patch)
  const sep = config.separator || '_'

  return (
    <Stack gap="sm">
      <Text size="sm" c="dimmed">
        {unflatten
          ? `Rebuilds nested objects from joined keys: a${sep}b${sep}c becomes {"a":{"b":{"c":…}}}. Use the separator the record was flattened with — "_" also splits snake_case names.`
          : `Turns nested objects into one level of joined keys: {"a":{"b":{"c":…}}} becomes a${sep}b${sep}c. Two fields that would join to the same key fail the record.`}
      </Text>
      <Group grow align="flex-start">
        <TextInput
          label="Separator"
          placeholder="_"
          description="Joins the keys"
          value={config.separator ?? ''}
          onChange={(e) => set({ separator: e.currentTarget.value })}
        />
        <TextInput
          label="Max depth"
          placeholder="64"
          description={unflatten ? 'Splits a key at most this many times' : 'Deeper objects are kept whole'}
          inputMode="numeric"
          value={config.maxDepth ?? ''}
          onChange={(e) => set({ maxDepth: e.currentTarget.value.replace(/\D/g, '') })}
        />
      </Group>
      <Input.Wrapper label="Arrays" description={unflatten ? 'Keys 0, 1, 2… under one name' : 'An array inside the record'}>
        <SegmentedControl
          fullWidth
          mt={4}
          value={config.arrays === 'keep' ? 'keep' : 'index'}
          onChange={(v) => set({ arrays: v })}
          data={[
            { value: 'index', label: unflatten ? 'Rebuild as arrays' : 'Index keys (a_0, a_1)' },
            { value: 'keep', label: unflatten ? 'Keep as objects' : 'Keep as arrays' },
          ]}
        />
      </Input.Wrapper>
      <Autocomplete
        label="Field"
        placeholder="Whole record"
        description={`Optional: ${unflatten ? 'unflatten' : 'flatten'} only this object field`}
        data={fieldPaths}
        value={config.field ?? ''}
        onChange={(v) => set({ field: v })}
      />
      {config.field && (
        <TextInput
          label="Target field"
          placeholder={config.field}
          description="Where the result goes; defaults to the field itself"
          value={config.targetField ?? ''}
          onChange={(e) => set({ targetField: e.currentTarget.value })}
        />
      )}
    </Stack>
  )
}
