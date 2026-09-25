import { useMemo, useState, type ReactNode } from 'react'
import { Button, Group, Select, Stack, Text, TextInput } from '@mantine/core'
import { findDateFormat, type DateFormatGroup } from './dateFormatOptions'
import { goLayoutProblem } from './goLayoutLint'

// The picker's own entry for a layout the list does not have. No listed layout
// can equal it: every one is written with Go's reference date.
const CUSTOM = 'custom'

interface DateFormatPickerProps {
  label: string
  description: ReactNode
  /** The entry that means "no layout", which the row stores as ''. */
  none: { value: string; label: string; hint: string }
  formats: DateFormatGroup[]
  /** Names the text box a custom layout is typed into. */
  customLabel: string
  value: string
  onChange: (layout: string) => void
}

/**
 * Chooses a Go layout by the text it produces. The list covers the shapes
 * operators ask for; anything else is a custom layout, typed, and checked for
 * the two mistakes that Go accepts without complaint (see goLayoutProblem).
 */
export function DateFormatPicker({ label, description, none, formats, customLabel, value, onChange }: DateFormatPickerProps) {
  // Picking "Custom layout…" has to stay picked while the layout is still empty
  // or, mid-word, happens to spell a listed format -- neither of which the
  // stored value can say.
  const [customPicked, setCustomPicked] = useState(false)
  const custom = customPicked || (value !== '' && !findDateFormat(formats, value))
  const selected = custom ? CUSTOM : value === '' ? none.value : value
  const problem = custom ? goLayoutProblem(value, formats) : null

  const { data, hints } = useMemo(() => {
    const hintByValue = new Map<string, string>([
      [none.value, none.hint],
      [CUSTOM, 'Write a Go layout'],
    ])
    for (const group of formats) for (const o of group.items) hintByValue.set(o.layout, o.pattern)
    return {
      hints: hintByValue,
      data: [
        { value: none.value, label: none.label },
        ...formats.map((group) => ({
          group: group.group,
          items: group.items.map((o) => ({ value: o.layout, label: o.example })),
        })),
        { value: CUSTOM, label: 'Custom layout…' },
      ],
    }
  }, [formats, none])

  return (
    <Stack gap={6}>
      <Select
        label={label}
        description={description}
        data={data}
        value={selected}
        allowDeselect={false}
        maxDropdownHeight={320}
        renderOption={({ option }) => (
          <Group justify="space-between" wrap="nowrap" gap="md" w="100%">
            <Text size="sm">{option.label}</Text>
            <Text size="xs" c="dimmed">
              {hints.get(option.value)}
            </Text>
          </Group>
        )}
        onChange={(next) => {
          if (next === CUSTOM) {
            // Starts from whatever was picked, so a listed format is the
            // starting point for a variation on it rather than a blank box.
            setCustomPicked(true)
            return
          }
          setCustomPicked(false)
          onChange(!next || next === none.value ? '' : next)
        }}
      />

      {custom && (
        <TextInput
          label={customLabel}
          placeholder="02 January 2006"
          value={value}
          onChange={(e) => onChange(e.currentTarget.value)}
          description="Write Go's reference date, Mon Jan 2 15:04:05 MST 2006, the way yours look: 2006 year · 01, Jan or January month · 02 day · Monday weekday · 15 hour (03 with PM) · 04 minute · 05 second."
          error={problem?.message}
          // The layout alone is code; its label and help text are prose.
          styles={{ input: { fontFamily: 'var(--mantine-font-family-monospace)' } }}
        />
      )}

      {problem?.fix && (
        <Button
          variant="light"
          size="xs"
          style={{ alignSelf: 'flex-start' }}
          onClick={() => {
            setCustomPicked(false)
            onChange(problem.fix!.layout)
          }}
        >
          Use {problem.fix.label}
        </Button>
      )}
    </Stack>
  )
}
