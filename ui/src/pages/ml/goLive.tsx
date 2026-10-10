import { Group, NumberInput, Radio, Select, Stack, Text } from '@mantine/core'
import type { GoLive } from '@/lib/mlModels'

/** The metrics a go-live rule can check; "score" is accuracy or R². */
export const GO_LIVE_METRICS = [
  { value: 'score', label: 'Score (accuracy, or R² for numbers)' },
  { value: 'accuracy', label: 'Accuracy' },
  { value: 'f1', label: 'F1 (macro)' },
  { value: 'roc_auc', label: 'ROC AUC (two classes)' },
  { value: 'r2', label: 'R²' },
]

export const DEFAULT_GO_LIVE_MIN = 0.8

/**
 * Whether a newly trained version is put live: never (a person promotes it),
 * always, or when a metric reaches a minimum.
 */
export function GoLiveField({ value, onChange }: { value: GoLive; onChange: (v: GoLive) => void }) {
  return (
    <Stack gap="xs">
      <Radio.Group
        label="Put the new version live"
        value={value.mode}
        onChange={(mode) => onChange(mode === 'if'
          ? { mode: 'if', metric: value.metric ?? 'score', min: value.min ?? DEFAULT_GO_LIVE_MIN }
          : { mode: mode as GoLive['mode'] })}
      >
        <Stack gap={6} mt={4}>
          <Radio value="never" label="Only when I put it live" />
          <Radio value="if" label="If it scores at least a minimum" />
          <Radio value="always" label="Always" />
        </Stack>
      </Radio.Group>
      {value.mode === 'if' && (
        <Group grow align="flex-start">
          <Select label="Metric" data={GO_LIVE_METRICS} value={value.metric ?? 'score'} allowDeselect={false}
            onChange={(metric) => onChange({ ...value, metric: metric ?? 'score' })} />
          <NumberInput label="Minimum" min={0} step={0.05} decimalScale={4} value={value.min ?? DEFAULT_GO_LIVE_MIN}
            onChange={(n) => onChange({ ...value, min: typeof n === 'number' ? n : undefined })} />
        </Group>
      )}
      <Text size="xs" c="dimmed">
        A version that is not put live is kept; you can put it live later from the model&apos;s versions.
      </Text>
    </Stack>
  )
}
