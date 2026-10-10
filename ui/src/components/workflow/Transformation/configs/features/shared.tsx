import { Autocomplete, Group, NumberInput, SegmentedControl, Select, Stack, Switch, Text, TextInput } from '@mantine/core'

/**
 * Pieces the feature-engineering editors share. Every editor saves the config
 * pkg/comm/transformer/features reads; see docs/ml.md.
 */

export interface FeatureConfigProps {
  config: any
  updateNodeConfig: (id: string, config: any) => void
  nodeId: string
  fieldPaths?: string[]
}

/** A NumberInput's value as a number, or undefined when it is blank. */
export const num = (v: number | string): number | undefined => (typeof v === 'number' ? v : undefined)

export function FieldInput({
  label = 'Field',
  description,
  value,
  onChange,
  fieldPaths,
  required = true,
}: {
  label?: string
  description?: string
  value: string | undefined
  onChange: (v: string) => void
  fieldPaths: string[]
  required?: boolean
}) {
  return (
    <Autocomplete
      label={label}
      description={description}
      placeholder="e.g. amount"
      data={fieldPaths}
      value={value ?? ''}
      onChange={onChange}
      required={required}
    />
  )
}

/** onMissing: what happens to a record without a usable value. */
export function OnMissingSelect({ value, onChange }: { value: string | undefined; onChange: (v: string) => void }) {
  return (
    <Select
      label="Missing or unusable value"
      description="A record whose field is absent, or not a number where one is needed."
      data={[
        { value: 'fail', label: 'Fail the record (the node’s On Error decides)' },
        { value: 'skip', label: 'Leave the record unchanged' },
      ]}
      value={value || 'fail'}
      onChange={(v) => onChange(v ?? 'fail')}
      allowDeselect={false}
    />
  )
}

/**
 * The per-key window rolling and anomaly_score keep: the last N events, or
 * the events of a time span, bounded per key and in the number of keys.
 */
export function WindowFields({ config, set, fieldPaths }: {
  config: any
  set: (patch: Record<string, unknown>) => void
  fieldPaths: string[]
}) {
  const byTime = config.windowType === 'time'
  return (
    <Stack gap="sm">
      <Autocomplete
        label="Key by"
        description="Keeps a separate window per value, e.g. per customer. Empty keeps one window for every record."
        placeholder="e.g. customer_id"
        data={fieldPaths}
        value={config.keyBy ?? ''}
        onChange={(v) => set({ keyBy: v })}
      />
      <Stack gap={4}>
        <Text size="sm" fw={500}>Window</Text>
        <SegmentedControl
          data={[
            { label: 'Last N events', value: 'count' },
            { label: 'Time window', value: 'time' },
          ]}
          value={byTime ? 'time' : 'count'}
          onChange={(v) => set({ windowType: v })}
        />
      </Stack>
      {byTime ? (
        <Group grow align="flex-start">
          <TextInput
            label="Window length"
            description="A duration: 30s, 5m, 1h."
            placeholder="5m"
            value={config.window ?? ''}
            onChange={(e) => set({ window: e.currentTarget.value })}
            required
          />
          <NumberInput
            label="Max events per key"
            description="Oldest dropped beyond this."
            placeholder="1000"
            min={1}
            max={100000}
            value={config.maxEvents ?? ''}
            onChange={(v) => set({ maxEvents: num(v) })}
          />
        </Group>
      ) : (
        <NumberInput
          label="Events in the window"
          description="The last N records of each key."
          placeholder="e.g. 20"
          min={1}
          max={100000}
          value={config.size ?? ''}
          onChange={(v) => set({ size: num(v) })}
          required
        />
      )}
      <NumberInput
        label="Keys kept in memory"
        description="The key seen least recently is dropped first."
        placeholder="10000"
        min={1}
        max={1000000}
        value={config.maxKeys ?? ''}
        onChange={(v) => set({ maxKeys: num(v) })}
      />
      <Switch
        label="Keep windows across restarts"
        description="Saves each key's window to the state store after every record, and reads it back after a restart or once a dropped key returns. Without it, windows start empty after a restart."
        checked={config.persistent === true || config.persistent === 'true'}
        onChange={(e) => set({ persistent: e.currentTarget.checked })}
      />
    </Stack>
  )
}
