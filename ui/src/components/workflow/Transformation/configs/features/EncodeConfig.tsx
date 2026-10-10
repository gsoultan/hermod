import { JsonInput, NumberInput, SegmentedControl, Select, Stack, Switch, TagsInput, Text, TextInput } from '@mantine/core'
import { type FeatureConfigProps, FieldInput, OnMissingSelect, num } from './shared'

/** categories is saved as a list; a hand-written workflow may hold text. */
function categoryList(v: unknown): string[] {
  if (Array.isArray(v)) return v.map(String)
  if (typeof v !== 'string' || v.trim() === '') return []
  try {
    const parsed = JSON.parse(v)
    if (Array.isArray(parsed)) return parsed.map(String)
  } catch {
    // Not JSON: comma-separated text.
  }
  return v.split(',').map((s) => s.trim()).filter(Boolean)
}

/**
 * Encode: turn a category into numbers — one 0/1 field per known category, an
 * integer per category, or a stable hash bucket.
 */
export function EncodeConfig({ config, updateNodeConfig, nodeId, fieldPaths = [] }: FeatureConfigProps) {
  const set = (patch: Record<string, unknown>) => updateNodeConfig(nodeId, patch)
  const method = config.method || 'onehot'
  const field = config.field || 'field'
  const prefix = config.prefix || `${field}_`
  const withOther = config.otherBucket !== false && config.otherBucket !== 'false'

  return (
    <Stack gap="sm">
      <FieldInput value={config.field} onChange={(v) => set({ field: v })} fieldPaths={fieldPaths} />

      <Stack gap={4}>
        <Text size="sm" fw={500}>Encoding</Text>
        <SegmentedControl
          data={[
            { label: 'One-hot', value: 'onehot' },
            { label: 'Label', value: 'label' },
            { label: 'Hash', value: 'hash' },
          ]}
          value={method}
          onChange={(v) => set({ method: v })}
        />
      </Stack>

      {method === 'onehot' && (
        <>
          <TagsInput
            label="Categories"
            description="The categories seen in training, in order. Each becomes a 0/1 field."
            placeholder="Type a category and press Enter"
            value={categoryList(config.categories)}
            onChange={(v) => set({ categories: v })}
            clearable
          />
          <TextInput
            label="Field prefix"
            placeholder={`${field}_`}
            value={config.prefix ?? ''}
            onChange={(e) => set({ prefix: e.currentTarget.value })}
          />
          <Switch
            label={`"Other" bucket (${prefix}other)`}
            description="Set to 1 for a value not in the list. Off, such a value writes all zeros."
            checked={withOther}
            onChange={(e) => set({ otherBucket: e.currentTarget.checked })}
          />
        </>
      )}

      {method === 'label' && (
        <>
          <JsonInput
            label="Mapping (JSON)"
            description="Each category's integer, as used in training."
            placeholder='{"S": 0, "M": 1, "L": 2}'
            value={typeof config.mapping === 'string' ? config.mapping : config.mapping ? JSON.stringify(config.mapping) : ''}
            onChange={(v) => set({ mapping: v })}
            validationError="Not valid JSON"
            autosize
            minRows={3}
          />
          <Select
            label="Unknown category"
            data={[
              { value: 'value', label: 'Write the unknown value' },
              { value: 'fail', label: 'Fail the record' },
            ]}
            value={config.onUnknown || 'value'}
            onChange={(v) => set({ onUnknown: v ?? 'value' })}
            allowDeselect={false}
          />
          {(config.onUnknown || 'value') === 'value' && (
            <NumberInput
              label="Unknown value"
              placeholder="-1"
              allowDecimal={false}
              value={config.unknownValue ?? ''}
              onChange={(v) => set({ unknownValue: num(v) })}
            />
          )}
        </>
      )}

      {method === 'hash' && (
        <NumberInput
          label="Buckets"
          description="FNV-1a hash of the value, modulo this. Stable across restarts and versions."
          placeholder="e.g. 32"
          min={1}
          max={16777216}
          allowDecimal={false}
          value={config.buckets ?? ''}
          onChange={(v) => set({ buckets: num(v) })}
          required
        />
      )}

      {method !== 'onehot' && (
        <TextInput
          label="Target field"
          placeholder={`${field}_${method === 'hash' ? 'bucket' : 'label'}`}
          value={config.targetField ?? ''}
          onChange={(e) => set({ targetField: e.currentTarget.value })}
        />
      )}
      <OnMissingSelect value={config.onMissing} onChange={(v) => set({ onMissing: v })} />
    </Stack>
  )
}
