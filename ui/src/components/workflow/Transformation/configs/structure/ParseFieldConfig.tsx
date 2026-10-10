import { Autocomplete, Group, Input, SegmentedControl, Stack, Switch, Text, TextInput } from '@mantine/core'

interface ParseFieldConfigProps {
  config: any
  updateNodeConfig: (id: string, config: any) => void
  nodeId: string
  fieldPaths?: string[]
}

const FORMATS = [
  { value: 'json', label: 'JSON' },
  { value: 'csv', label: 'CSV' },
  { value: 'xml', label: 'XML' },
  { value: 'kv', label: 'key=value' },
]

const WHAT: Record<string, string> = {
  json: 'Any JSON value.',
  csv: 'Always an array of rows: objects when there are headers, lists of values when there are none.',
  xml: 'Elements become objects, attributes "@name", repeated elements arrays. Document type declarations are refused.',
  kv: 'Pairs such as level=info msg="disk full". Values stay text.',
}

/**
 * parse_field (pkg/comm/transformer/structure/parse_field.go): parse a text
 * field into structure. Saves field, format, targetField, maxBytes and the
 * format's own settings.
 */
export function ParseFieldConfig({ config, updateNodeConfig, nodeId, fieldPaths = [] }: ParseFieldConfigProps) {
  const set = (patch: Record<string, unknown>) => updateNodeConfig(nodeId, patch)
  const format: string = config.format || 'json'

  return (
    <Stack gap="sm">
      <Autocomplete
        label="Field"
        placeholder="e.g. body"
        description="The field holding the text"
        data={fieldPaths}
        value={config.field ?? ''}
        onChange={(v) => set({ field: v })}
        required
      />
      <Input.Wrapper label="Format" description={WHAT[format]}>
        <SegmentedControl fullWidth mt={4} value={format} onChange={(v) => set({ format: v })} data={FORMATS} />
      </Input.Wrapper>
      {format === 'csv' && (
        <>
          <Group grow align="flex-start">
            <TextInput
              label="Delimiter"
              placeholder=","
              description='"tab" for a tab'
              value={config.delimiter ?? ''}
              onChange={(e) => set({ delimiter: e.currentTarget.value })}
            />
            <TextInput
              label="Headers"
              placeholder="id, name, email"
              description="Column names, comma-separated"
              value={config.headers ?? ''}
              onChange={(e) => set({ headers: e.currentTarget.value })}
            />
          </Group>
          <Switch
            label="The first line names the columns"
            checked={config.hasHeader === true || config.hasHeader === 'true'}
            onChange={(e) => set({ hasHeader: e.currentTarget.checked })}
          />
        </>
      )}
      {format === 'kv' && (
        <Group grow align="flex-start">
          <TextInput
            label="Pair delimiter"
            placeholder="space"
            description="Between pairs; empty means spaces"
            value={config.pairDelimiter ?? ''}
            onChange={(e) => set({ pairDelimiter: e.currentTarget.value })}
          />
          <TextInput
            label="Key/value separator"
            placeholder="="
            value={config.kvSeparator ?? ''}
            onChange={(e) => set({ kvSeparator: e.currentTarget.value })}
          />
        </Group>
      )}
      <Group grow align="flex-start">
        <TextInput
          label="Target field"
          placeholder={config.field || 'the field itself'}
          description="Where the result goes"
          value={config.targetField ?? ''}
          onChange={(e) => set({ targetField: e.currentTarget.value })}
        />
        <TextInput
          label="Max bytes"
          placeholder="1048576"
          description="Longer text fails the record (at most 16 MiB)"
          inputMode="numeric"
          value={config.maxBytes ?? ''}
          onChange={(e) => set({ maxBytes: e.currentTarget.value.replace(/\D/g, '') })}
        />
      </Group>
      <Text size="xs" c="dimmed">Text that does not parse fails the record; the node's On Error setting decides what follows.</Text>
    </Stack>
  )
}
