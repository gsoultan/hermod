import { Group, Select, Text, type ComboboxData } from '@mantine/core'

// The picker's entry for "no zone", which the row stores as ''. No zone name
// can equal it: every IANA name holds a slash, and UTC is spelled out.
const OWN_ZONE = 'own'
const OWN_ZONE_LABEL = "Keep each value's own zone"

// Every IANA zone the browser knows, read once. The browser's list rather than
// a copy, because a hand-kept list goes stale with each tzdata release. Every
// name in it loads in the engine's embedded zone database (all 418 in Node 24's
// list were checked). UTC is added because browsers do not list it as a zone.
const BROWSER_ZONES: string[] =
  typeof Intl.supportedValuesOf === 'function' ? Intl.supportedValuesOf('timeZone') : []

const THIS_BROWSER = Intl.DateTimeFormat().resolvedOptions().timeZone

const dataByStoredZone = new Map<string, ComboboxData>()

function zoneData(stored: string): ComboboxData {
  const cached = dataByStoredZone.get(stored)
  if (cached) return cached

  // The operator's own zone is usually the business's, so it is offered first.
  const suggested = [...new Set([THIS_BROWSER, 'UTC'].filter(Boolean))]
  const listed = new Set([...suggested, ...BROWSER_ZONES])
  const data: ComboboxData = [
    { value: OWN_ZONE, label: OWN_ZONE_LABEL },
    // Stored config outlives the browser that wrote it: a zone set through the
    // API, or saved from a browser that spells it differently (Asia/Kolkata
    // here is Asia/Calcutta there), opens as itself instead of as the default.
    ...(stored && !listed.has(stored) ? [{ value: stored, label: stored }] : []),
    { group: 'Suggested', items: suggested.map((z) => ({ value: z, label: z })) },
    {
      group: 'All zones',
      items: BROWSER_ZONES.filter((z) => !suggested.includes(z)).map((z) => ({ value: z, label: z })),
    },
  ]
  dataByStoredZone.set(stored, data)
  return data
}

interface TimeZonePickerProps {
  /** The row's IANA zone; empty keeps each value in its own zone. */
  value: string
  onChange: (zone: string) => void
}

/**
 * The zone a date row is written in, and the zone a value that carries none of
 * its own is read in. There are over four hundred, so the list is searched
 * rather than scrolled.
 */
export function TimeZonePicker({ value, onChange }: TimeZonePickerProps) {
  return (
    <Select
      label="Time zone"
      description="The zone dates are written in, and the zone a value with no zone of its own (2026-09-18 20:00) is read in. Keep each value's own zone to write dates exactly as they arrive."
      data={zoneData(value)}
      value={value || OWN_ZONE}
      searchable
      limit={60}
      maxDropdownHeight={280}
      nothingFoundMessage="No zone by that name"
      allowDeselect={false}
      renderOption={({ option }) => (
        <Group justify="space-between" wrap="nowrap" gap="md" w="100%">
          <Text size="sm">{option.label}</Text>
          {option.value === THIS_BROWSER && (
            <Text size="xs" c="dimmed">
              this browser
            </Text>
          )}
        </Group>
      )}
      onChange={(next) => onChange(!next || next === OWN_ZONE ? '' : next)}
    />
  )
}
