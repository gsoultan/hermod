import { useState } from 'react'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { vi } from 'vitest'
import { DataConversionConfig } from '@/components/workflow/Transformation/configs/data/DataConversionConfig'
import {
  INPUT_DATE_FORMATS,
  OUTPUT_DATE_FORMATS,
} from '@/components/workflow/Transformation/configs/data/dateFormat/dateFormatOptions'
import { goLayoutProblem } from '@/components/workflow/Transformation/configs/data/dateFormat/goLayoutLint'

// A date row used to have one free-text "Date Format" field, and it only ever
// described how a value is *read*. An operator who set it to "02 January 2026"
// to get "18 September 2026" got 2026-09-18T04:30:57.333046Z straight back --
// and the layout was itself mistyped, since Go spells every year 2006. The row
// now has two pickers, Output format and Input format, each a list of formats
// shown as the text they produce, with a custom layout behind a guard.
//
// The keys written here are the ones parseConversions reads in
// pkg/comm/transformer/core/conversion.go: `outputFormat` and `format`.
describe('data_conversion date formats', () => {
  // The real store merges each patch into the node, so the next render sees
  // what the last one wrote. A bare vi.fn() would freeze the config and make a
  // typed layout arrive one character at a time.
  function Harness({ initial, onWrite }: { initial: any; onWrite: (patch: any) => void }) {
    const [config, setConfig] = useState(initial)
    return (
      <DataConversionConfig
        config={config}
        updateNodeConfig={(_id: string, patch: any) => {
          setConfig((c: any) => ({ ...c, ...patch }))
          onWrite(patch)
        }}
        nodeId="n1"
        fieldPaths={[]}
      />
    )
  }

  const renderRow = (row: Record<string, unknown>) => {
    const onWrite = vi.fn()
    render(
      <MantineProvider>
        <Harness initial={{ conversions: [{ field: 'created_at', targetType: 'date', ...row }] }} onWrite={onWrite} />
      </MantineProvider>
    )
    return onWrite
  }

  const row = () => within(screen.getByTestId('conversion-row-0'))
  const written = (fn: ReturnType<typeof vi.fn>) => fn.mock.calls.at(-1)?.[0].conversions[0]

  // jsdom has no layout, so Mantine renders an open dropdown at display:none and
  // its options leave the accessibility tree; see dataConversionArrayType.test.tsx.
  const openDropdown = async (user: ReturnType<typeof userEvent.setup>, name: RegExp) => {
    const input = row().getByRole('combobox', { name })
    await user.click(input)
    expect(input).toHaveAttribute('aria-expanded', 'true')
    const dropdown = document.getElementById(input.getAttribute('aria-controls') || '')
    expect(dropdown).not.toBeNull()
    return dropdown as HTMLElement
  }

  const choose = async (user: ReturnType<typeof userEvent.setup>, picker: RegExp, text: string) => {
    const dropdown = await openDropdown(user, picker)
    const option = within(dropdown).getByText(text, { exact: true }).closest('[role="option"]')
    expect(option).not.toBeNull()
    await user.click(option as HTMLElement)
  }

  it('offers an output format and an input format instead of a typed layout', () => {
    renderRow({})
    expect(row().getByRole('combobox', { name: /output format/i })).toHaveValue('Keep as a date/time value')
    expect(row().getByRole('combobox', { name: /input format/i })).toHaveValue('Auto-detect (ISO 8601)')
    expect(row().queryByRole('textbox', { name: /format|layout/i })).not.toBeInTheDocument()
  })

  it('writes the layout behind the output format picked', async () => {
    const user = userEvent.setup()
    const onWrite = renderRow({})
    await choose(user, /output format/i, '18 September 2026')
    expect(written(onWrite)).toMatchObject({ field: 'created_at', targetType: 'date', outputFormat: '02 January 2006' })
    expect(row().getByRole('combobox', { name: /output format/i })).toHaveValue('18 September 2026')
  })

  it('shows each format with the letter codes it corresponds to', async () => {
    const user = userEvent.setup()
    renderRow({})
    const dropdown = await openDropdown(user, /output format/i)
    const option = within(dropdown).getByText('18 September 2026', { exact: true }).closest('[role="option"]')
    expect(option).toHaveTextContent('DD MMMM YYYY')
  })

  it('writes an empty output format for "Keep as a date/time value"', async () => {
    const user = userEvent.setup()
    const onWrite = renderRow({ outputFormat: '02 January 2006' })
    await choose(user, /output format/i, 'Keep as a date/time value')
    expect(written(onWrite).outputFormat).toBe('')
  })

  it('writes the layout behind the input format picked, and nothing for auto-detect', async () => {
    const user = userEvent.setup()
    const onWrite = renderRow({})
    await choose(user, /input format/i, '18/09/2026')
    expect(written(onWrite).format).toBe('02/01/2006')
    await choose(user, /input format/i, 'Auto-detect (ISO 8601)')
    expect(written(onWrite).format).toBe('')
  })

  it('shows a stored layout as the text it produces', () => {
    renderRow({ format: '02/01/2006', outputFormat: '2006-01-02' })
    expect(row().getByRole('combobox', { name: /output format/i })).toHaveValue('2026-09-18')
    expect(row().getByRole('combobox', { name: /input format/i })).toHaveValue('18/09/2026')
  })

  // Stored rows hold whatever was typed into the old field. A layout the list
  // does not have must still open looking like what it does.
  it('opens a layout the list does not have as a custom layout', () => {
    renderRow({ format: '02-Jan-06 15h04' })
    expect(row().getByRole('combobox', { name: /input format/i })).toHaveValue('Custom layout…')
    expect(row().getByRole('textbox', { name: /custom input layout/i })).toHaveValue('02-Jan-06 15h04')
  })

  it('starts a custom layout from the format already picked', async () => {
    const user = userEvent.setup()
    const onWrite = renderRow({ outputFormat: '02 January 2006' })
    await choose(user, /output format/i, 'Custom layout…')
    const layout = row().getByRole('textbox', { name: /custom output layout/i })
    expect(layout).toHaveValue('02 January 2006')
    await user.type(layout, ' 15:04')
    expect(written(onWrite).outputFormat).toBe('02 January 2006 15:04')
  })

  // Typing a custom layout passes through listed ones on the way -- "2006-01-02"
  // on the way to "2006-01-02 15:04". Switching the picker to that format mid-word
  // would take the text box away while it is being typed in.
  it('keeps a custom layout open while it is typed, even through a listed format', async () => {
    const user = userEvent.setup()
    const onWrite = renderRow({})
    await choose(user, /output format/i, 'Custom layout…')
    await user.type(row().getByRole('textbox', { name: /custom output layout/i }), '2006-01-02 15:04')
    expect(row().getByRole('textbox', { name: /custom output layout/i })).toHaveValue('2006-01-02 15:04')
    expect(written(onWrite).outputFormat).toBe('2006-01-02 15:04')
  })

  describe('typo guard', () => {
    it('flags a year that is not 2006 and offers the corrected layout', async () => {
      const user = userEvent.setup()
      const onWrite = renderRow({ outputFormat: '02 January 2026' })
      const layout = row().getByRole('textbox', { name: /custom output layout/i })
      expect(layout).toHaveAttribute('aria-invalid', 'true')
      expect(row().getByText(/2026/, { selector: '.mantine-TextInput-error' })).toHaveTextContent('2006')

      await user.click(row().getByRole('button', { name: /use 02 january 2006/i }))
      expect(written(onWrite).outputFormat).toBe('02 January 2006')
      // The corrected layout is a listed format, so the picker shows it as one.
      expect(row().getByRole('combobox', { name: /output format/i })).toHaveValue('18 September 2026')
      expect(row().queryByRole('textbox', { name: /custom output layout/i })).not.toBeInTheDocument()
    })

    it('flags letter codes and offers the listed format they spell', async () => {
      const user = userEvent.setup()
      const onWrite = renderRow({ outputFormat: 'DD/MM/YYYY' })
      expect(row().getByRole('textbox', { name: /custom output layout/i })).toHaveAttribute('aria-invalid', 'true')
      await user.click(row().getByRole('button', { name: /use 02\/01\/2006/i }))
      expect(written(onWrite).outputFormat).toBe('02/01/2006')
    })

    it('leaves a layout that is fine alone', () => {
      renderRow({ format: '02-Jan-06 15h04' })
      expect(row().getByRole('textbox', { name: /custom input layout/i })).not.toHaveAttribute('aria-invalid', 'true')
      expect(row().queryByRole('button', { name: /^use /i })).not.toBeInTheDocument()
    })
  })

  // The confusion this replaces: a format that only affects reading, on a row
  // whose output is unchanged. Say so where the operator is looking.
  it('says an input format alone does not change what is written', () => {
    renderRow({ format: '02 January 2006' })
    expect(row().getByText(/only changes how values are read/i)).toBeInTheDocument()
  })

  // A date written as text is written in the value's own zone unless the row
  // names one, and 2026-09-18T20:00:00Z is already the 19th in Jakarta. The key
  // written is the one parseConversions reads: `timeZone`.
  describe('time zone', () => {
    const zoneInput = () => row().getByRole('combobox', { name: /time zone/i })

    it("defaults to each value's own zone", () => {
      renderRow({})
      expect(zoneInput()).toHaveValue("Keep each value's own zone")
    })

    it('writes the zone picked, found by typing part of its name', async () => {
      const user = userEvent.setup()
      const onWrite = renderRow({})
      await user.click(zoneInput())
      // The box shows the current choice; searching starts from an empty box.
      await user.clear(zoneInput())
      await user.type(zoneInput(), 'Jakarta')
      const dropdown = document.getElementById(zoneInput().getAttribute('aria-controls') || '') as HTMLElement
      await user.click(within(dropdown).getByText('Asia/Jakarta', { exact: true }).closest('[role="option"]') as HTMLElement)
      expect(written(onWrite).timeZone).toBe('Asia/Jakarta')
      expect(zoneInput()).toHaveValue('Asia/Jakarta')
    })

    it("writes nothing for each value's own zone", async () => {
      const user = userEvent.setup()
      const onWrite = renderRow({ timeZone: 'Asia/Jakarta' })
      await user.click(zoneInput())
      await user.clear(zoneInput())
      const dropdown = document.getElementById(zoneInput().getAttribute('aria-controls') || '') as HTMLElement
      await user.click(within(dropdown).getByText("Keep each value's own zone", { exact: true }).closest('[role="option"]') as HTMLElement)
      expect(written(onWrite).timeZone).toBe('')
    })

    it('offers UTC, which the browser does not list as a zone', async () => {
      const user = userEvent.setup()
      renderRow({})
      await user.click(zoneInput())
      // The box shows the current choice; searching starts from an empty box.
      await user.clear(zoneInput())
      await user.type(zoneInput(), 'UTC')
      const dropdown = document.getElementById(zoneInput().getAttribute('aria-controls') || '') as HTMLElement
      expect(within(dropdown).getByText('UTC', { exact: true })).toBeInTheDocument()
    })

    // Stored config outlives the browser that wrote it: a zone typed through the
    // API, or saved from a browser whose list spells it differently, must still
    // open as itself rather than as the default.
    it('shows a stored zone the list does not have', () => {
      renderRow({ timeZone: 'Asia/Kolkata' })
      expect(zoneInput()).toHaveValue('Asia/Kolkata')
    })

    it('is offered only on a date row', () => {
      renderRow({ targetType: 'int', timeZone: 'Asia/Jakarta' })
      expect(row().queryByRole('combobox', { name: /time zone/i })).not.toBeInTheDocument()
    })
  })

  it('offers date formats only on a date row', () => {
    renderRow({ targetType: 'float', outputFormat: '2006-01-02', format: '02/01/2006' })
    expect(row().queryByRole('combobox', { name: /output format/i })).not.toBeInTheDocument()
    expect(row().queryByRole('combobox', { name: /input format/i })).not.toBeInTheDocument()
  })
})

describe('goLayoutProblem', () => {
  it.each([
    '2006-01-02',
    '20060102150405',
    // "2012" sits inside this day-month-year run; it is not a year, and
    // "correcting" it would write 02006006.
    '02012006',
    'Monday, 02 January 2006',
    'Mon Jan _2 15:04:05 MST 2006',
    '02-Jan-06 15h04',
    '',
  ])('accepts %j', (layout) => {
    expect(goLayoutProblem(layout, OUTPUT_DATE_FORMATS)).toBeNull()
  })

  it.each([
    ['02 January 2026', '02 January 2006'],
    ['2025-01-02', '2006-01-02'],
    ['02/01/1999 15:04', '02/01/2006 15:04'],
  ])('corrects the year in %j', (layout, fixed) => {
    const problem = goLayoutProblem(layout, OUTPUT_DATE_FORMATS)
    expect(problem?.message).toMatch(/2006/)
    expect(problem?.fix?.layout).toBe(fixed)
  })

  it.each([
    ['DD MMMM YYYY', '02 January 2006'],
    ['dd/MM/yyyy', '02/01/2006'],
    ['yyyy-MM-dd', '2006-01-02'],
    ['hh:mm A', '03:04 PM'],
  ])('translates the letter codes %j to the listed format', (layout, fixed) => {
    const problem = goLayoutProblem(layout, OUTPUT_DATE_FORMATS)
    expect(problem?.message).toMatch(/letter codes/i)
    expect(problem?.fix?.layout).toBe(fixed)
  })

  it('flags letter codes it cannot translate without inventing a fix', () => {
    const problem = goLayoutProblem('YYYY.MM.DD HH:mm', OUTPUT_DATE_FORMATS)
    expect(problem?.message).toMatch(/letter codes/i)
    expect(problem?.fix).toBeUndefined()
  })

  it('looks for the fix only among the formats of its own picker', () => {
    expect(goLayoutProblem('hh:mm A', INPUT_DATE_FORMATS)?.fix).toBeUndefined()
  })
})

describe('the date format lists', () => {
  // Mantine refuses to render a Select whose values repeat, and the pickers add
  // their own sentinel options next to these.
  it.each([
    ['input', INPUT_DATE_FORMATS],
    ['output', OUTPUT_DATE_FORMATS],
  ])('lists each %s layout once', (_name, formats) => {
    const layouts = formats.flatMap((g) => g.items.map((o) => o.layout))
    expect(new Set(layouts).size).toBe(layouts.length)
    for (const layout of layouts) expect(goLayoutProblem(layout, formats)).toBeNull()
  })
})
