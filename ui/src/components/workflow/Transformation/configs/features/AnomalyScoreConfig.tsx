import { Group, NumberInput, SegmentedControl, Stack, Text, TextInput } from '@mantine/core'
import { type FeatureConfigProps, FieldInput, OnMissingSelect, WindowFields, num } from './shared'

/**
 * Anomaly score: score each record's value against its key's recent values
 * (the window before it), and flag those above a threshold.
 */
export function AnomalyScoreConfig({ config, updateNodeConfig, nodeId, fieldPaths = [] }: FeatureConfigProps) {
  const set = (patch: Record<string, unknown>) => updateNodeConfig(nodeId, patch)
  const iqr = config.method === 'iqr'
  const field = config.field || 'field'

  return (
    <Stack gap="sm">
      <FieldInput value={config.field} onChange={(v) => set({ field: v })} fieldPaths={fieldPaths} />
      <Stack gap={4}>
        <Text size="sm" fw={500}>Method</Text>
        <SegmentedControl
          data={[
            { label: 'Z-score', value: 'zscore' },
            { label: 'IQR', value: 'iqr' },
          ]}
          value={iqr ? 'iqr' : 'zscore'}
          onChange={(v) => set({ method: v })}
        />
        <Text size="xs" c="dimmed">
          {iqr
            ? 'How many interquartile ranges the value lies outside [Q1, Q3]; 0 inside.'
            : 'How many standard deviations the value lies from the mean.'}
        </Text>
      </Stack>
      <Group grow align="flex-start">
        <NumberInput
          label="Threshold"
          description="A score above it is an anomaly."
          placeholder={iqr ? '1.5' : '3'}
          min={0}
          value={config.threshold ?? ''}
          onChange={(v) => set({ threshold: num(v) })}
        />
        <NumberInput
          label="Minimum history"
          description="Events a key needs before it is scored."
          placeholder="10"
          min={2}
          allowDecimal={false}
          value={config.minEvents ?? ''}
          onChange={(v) => set({ minEvents: num(v) })}
        />
      </Group>
      <WindowFields config={config} set={set} fieldPaths={fieldPaths} />
      <Group grow align="flex-start">
        <TextInput
          label="Score field"
          placeholder={`${field}_anomaly_score`}
          value={config.scoreField ?? ''}
          onChange={(e) => set({ scoreField: e.currentTarget.value })}
        />
        <TextInput
          label="Flag field"
          placeholder={`${field}_is_anomaly`}
          value={config.flagField ?? ''}
          onChange={(e) => set({ flagField: e.currentTarget.value })}
        />
      </Group>
      <OnMissingSelect value={config.onMissing} onChange={(v) => set({ onMissing: v })} />
    </Stack>
  )
}
