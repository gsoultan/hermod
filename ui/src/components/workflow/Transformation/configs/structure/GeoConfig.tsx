import { Autocomplete, Group, Input, SegmentedControl, Stack, Text, Textarea, TextInput } from '@mantine/core'
import type { ReactNode } from 'react'

interface GeoConfigProps {
  config: any
  updateNodeConfig: (id: string, config: any) => void
  nodeId: string
  fieldPaths?: string[]
}

/**
 * geo (pkg/comm/transformer/geo): the great-circle distance between two
 * points on the record, or whether a point lies in a GeoJSON polygon. No
 * geocoding. Saves operation, the coordinate fields, unit, polygon and
 * targetField.
 */
export function GeoConfig({ config, updateNodeConfig, nodeId, fieldPaths = [] }: GeoConfigProps) {
  const set = (patch: Record<string, unknown>) => updateNodeConfig(nodeId, patch)
  const within = config.operation === 'within'

  const field = (key: string, label: string, placeholder: string) => (
    <Autocomplete
      label={label}
      placeholder={placeholder}
      data={fieldPaths}
      value={config[key] ?? ''}
      onChange={(v) => set({ [key]: v })}
      required
    />
  )

  return (
    <Stack gap="sm">
      <Input.Wrapper label="Operation">
        <SegmentedControl
          fullWidth
          mt={4}
          value={within ? 'within' : 'distance'}
          onChange={(v) => set({ operation: v })}
          data={[
            { value: 'distance', label: 'Distance' },
            { value: 'within', label: 'Inside polygon' },
          ]}
        />
      </Input.Wrapper>
      {within ? <WithinFields config={config} set={set} field={field} /> : (
        <>
          <Group grow align="flex-start">
            {field('lat1Field', 'First point latitude', 'e.g. pickup.lat')}
            {field('lon1Field', 'First point longitude', 'e.g. pickup.lon')}
          </Group>
          <Group grow align="flex-start">
            {field('lat2Field', 'Second point latitude', 'e.g. dropoff.lat')}
            {field('lon2Field', 'Second point longitude', 'e.g. dropoff.lon')}
          </Group>
          <Input.Wrapper label="Unit">
            <SegmentedControl
              fullWidth
              mt={4}
              value={config.unit || 'km'}
              onChange={(v) => set({ unit: v })}
              data={[
                { value: 'km', label: 'Kilometres' },
                { value: 'mi', label: 'Miles' },
                { value: 'm', label: 'Metres' },
              ]}
            />
          </Input.Wrapper>
        </>
      )}
      <TextInput
        label="Target field"
        placeholder={within ? 'inside' : 'distance'}
        description={within ? 'Receives true or false' : 'Receives the distance as a number'}
        value={config.targetField ?? ''}
        onChange={(e) => set({ targetField: e.currentTarget.value })}
      />
      <Text size="xs" c="dimmed">
        Coordinates are decimal degrees. A missing or out-of-range coordinate fails the record rather than being read as 0.
      </Text>
    </Stack>
  )
}

function WithinFields({ config, set, field }: {
  config: any
  set: (patch: Record<string, unknown>) => void
  field: (key: string, label: string, placeholder: string) => ReactNode
}) {
  return (
    <>
      <Group grow align="flex-start">
        {field('latField', 'Latitude', 'e.g. lat')}
        {field('lonField', 'Longitude', 'e.g. lon')}
      </Group>
      <Textarea
        label="Polygon"
        description="A GeoJSON Polygon or MultiPolygon, or a Feature holding one. Holes are honoured."
        placeholder='{"type":"Polygon","coordinates":[[[lon,lat],…]]}'
        autosize
        minRows={4}
        maxRows={12}
        styles={{ input: { fontFamily: 'var(--mantine-font-family-monospace)' } }}
        value={config.polygon ?? ''}
        onChange={(e) => set({ polygon: e.currentTarget.value })}
        error={polygonError(config.polygon)}
        required
      />
    </>
  )
}

/** What is wrong with the polygon text, before the engine says so on the first record. */
function polygonError(text: unknown): string | null {
  if (typeof text !== 'string' || text.trim() === '') return null
  try {
    const g = JSON.parse(text)
    const type = g?.type === 'Feature' ? g?.geometry?.type : g?.type
    return type === 'Polygon' || type === 'MultiPolygon' ? null : 'GeoJSON type must be Polygon or MultiPolygon'
  } catch {
    return 'Not valid JSON'
  }
}
