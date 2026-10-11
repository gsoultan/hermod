import { Badge, Group, Radio, SimpleGrid, Stack, Text } from '@mantine/core'
import { DEFAULT_PRESET, PRESET_OPTIONS, type TrainPreset } from '@/lib/mlModels'

/**
 * How careful "auto" is: Quick, Balanced or Best quality, each with what it
 * does and roughly how long it takes. Disabled when an algorithm is picked by
 * hand, since the worker then trains only that one.
 */
export function PresetPicker({
  value, onChange, disabled = false,
}: {
  value: TrainPreset | undefined
  onChange: (preset: TrainPreset) => void
  disabled?: boolean
}) {
  return (
    <Radio.Group label="How careful should Hermod be?" value={value || DEFAULT_PRESET} onChange={(v) => onChange(v as TrainPreset)}
      description={disabled
        ? 'An algorithm is picked under Advanced, so Hermod trains only that one.'
        : 'Hermod tries several methods and keeps the one that does best.'}>
      <SimpleGrid cols={{ base: 1, sm: 3 }} mt="xs">
        {PRESET_OPTIONS.map((o) => (
          <Radio.Card key={o.value} value={o.value} radius="md" p="sm" disabled={disabled}
            style={disabled ? { opacity: 0.6, cursor: 'not-allowed' } : undefined}>
            <Group wrap="nowrap" align="flex-start" gap="sm">
              <Radio.Indicator mt={2} />
              <Stack gap={4}>
                <Group gap={6}>
                  <Text fw={600} size="sm">{o.label}</Text>
                  {o.value === DEFAULT_PRESET && <Badge size="xs" variant="light" tt="none">Recommended</Badge>}
                </Group>
                <Text size="xs" c="dimmed">{o.description}</Text>
                <Text size="xs">{o.time}</Text>
              </Stack>
            </Group>
          </Radio.Card>
        ))}
      </SimpleGrid>
    </Radio.Group>
  )
}
