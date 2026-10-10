import { Checkbox, Group, Stack, Text, TextInput } from '@mantine/core'
import { type FeatureConfigProps, FieldInput, OnMissingSelect, WindowFields } from './shared'

const FEATURES = ['count', 'sum', 'mean', 'std', 'min', 'max']

function featureList(v: unknown): string[] {
  if (Array.isArray(v)) return v.map(String)
  if (typeof v === 'string' && v.trim() !== '') return v.split(',').map((s) => s.trim())
  return FEATURES
}

/**
 * Rolling: count, sum, mean, std, min and max of a field over each key's
 * recent records, the current one included.
 */
export function RollingConfig({ config, updateNodeConfig, nodeId, fieldPaths = [] }: FeatureConfigProps) {
  const set = (patch: Record<string, unknown>) => updateNodeConfig(nodeId, patch)
  const chosen = featureList(config.features)
  const prefix = config.prefix || (config.field ? `${config.field}_` : 'rolling_')

  return (
    <Stack gap="sm">
      <FieldInput
        value={config.field}
        onChange={(v) => set({ field: v })}
        fieldPaths={fieldPaths}
        description="The number to summarise. Leave empty to only count events."
        required={false}
      />
      <WindowFields config={config} set={set} fieldPaths={fieldPaths} />
      <Checkbox.Group
        label="Features"
        description={`Each is written to ${prefix}<feature>, e.g. ${prefix}mean. std is the population standard deviation.`}
        value={chosen}
        error={chosen.length === 0 ? 'Choose at least one: with none, all six are written.' : undefined}
        // Saved in a fixed order, whatever order they were ticked in.
        onChange={(v) => set({ features: FEATURES.filter((f) => v.includes(f)) })}
      >
        <Group mt="xs" gap="md">
          {FEATURES.map((f) => (
            <Checkbox key={f} value={f} label={f} />
          ))}
        </Group>
      </Checkbox.Group>
      <TextInput
        label="Field prefix"
        placeholder={prefix}
        value={config.prefix ?? ''}
        onChange={(e) => set({ prefix: e.currentTarget.value })}
      />
      {config.field ? (
        <OnMissingSelect value={config.onMissing} onChange={(v) => set({ onMissing: v })} />
      ) : (
        <Text size="xs" c="dimmed">Without a field only count is available.</Text>
      )}
    </Stack>
  )
}
