import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { vi } from 'vitest'
import { DataConversionConfig } from '@/components/workflow/Transformation/configs/data/DataConversionConfig'

// data_conversion gained "array" and "uuid" target types so a scalar can be
// turned into a list (and back), which is what makes `IN ({{.field}})` usable
// in a SQL template. The editor is the only way to reach them, so the option
// list has to match the backend's switch in
// pkg/comm/transformer/core/conversion.go -- an option the backend rejects, or
// a conversion the editor cannot offer, are the same defect.
//
// The node holds a list of conversion rows, so every control here belongs to a
// row and is scoped to one.
describe('data_conversion array and uuid target types', () => {
  // jsdom has no layout, so floating-ui's hide() middleware reports the
  // reference as hidden and Mantine puts the open dropdown at display:none,
  // which takes its options out of the accessibility tree. aria-expanded is
  // what establishes that the dropdown opened; the options are then read out of
  // the dropdown element directly.
  const openDropdown = async (user: ReturnType<typeof userEvent.setup>, name: RegExp, row = 0) => {
    const input = within(screen.getByTestId(`conversion-row-${row}`)).getByRole('combobox', { name })
    await user.click(input)
    expect(input).toHaveAttribute('aria-expanded', 'true')
    const dropdown = document.getElementById(input.getAttribute('aria-controls') || '')
    expect(dropdown).not.toBeNull()
    return dropdown as HTMLElement
  }

  const optionLabels = (dropdown: HTMLElement) =>
    Array.from(dropdown.querySelectorAll('[role="option"]')).map((o) => o.textContent)

  const renderConfig = (config: any, updateNodeConfig = () => {}) =>
    render(
      <MantineProvider>
        <DataConversionConfig
          config={config}
          updateNodeConfig={updateNodeConfig}
          nodeId="n1"
          fieldPaths={[]}
        />
      </MantineProvider>
    )

  const row = (i = 0) => within(screen.getByTestId(`conversion-row-${i}`))

  // The row a write lands in, rather than the whole config patch: the patch
  // also carries the cleared pre-list keys, which is the migration's business
  // and is asserted in dataConversionMultiField.test.tsx.
  const writtenRow = (fn: ReturnType<typeof vi.fn>, i = 0) =>
    fn.mock.calls.at(-1)?.[1].conversions[i]

  it('offers Array, JSON and UUID alongside the scalar types', async () => {
    const user = userEvent.setup()
    renderConfig({})
    const dropdown = await openDropdown(user, /target type/i)
    expect(optionLabels(dropdown)).toEqual([
      'Integer', 'Float', 'String', 'Boolean', 'Date', 'Array', 'JSON / JSONB', 'UUID',
    ])
  })

  // A jsonb conversion takes no separator and no element type: it renders one
  // value as JSON text, it does not split or join anything.
  it('shows no separator or element type for a jsonb conversion', () => {
    renderConfig({ targetType: 'jsonb' })
    expect(row().queryByRole('textbox', { name: /separator/i })).not.toBeInTheDocument()
    expect(row().queryByRole('combobox', { name: /element type/i })).not.toBeInTheDocument()
  })

  // The value the option writes is the one convertScalar switches on.
  it('writes jsonb as the target type', async () => {
    const updateNodeConfig = vi.fn()
    const user = userEvent.setup()
    renderConfig({ targetType: 'string' }, updateNodeConfig)
    const dropdown = await openDropdown(user, /target type/i)
    const jsonOption = Array.from(dropdown.querySelectorAll('[role="option"]'))
      .find((o) => o.textContent === 'JSON / JSONB') as HTMLElement
    await user.click(jsonOption)
    expect(writtenRow(updateNodeConfig).targetType).toBe('jsonb')
  })

  it('reveals separator and element type for an array conversion', () => {
    renderConfig({ targetType: 'array' })
    expect(row().getByRole('textbox', { name: /separator/i })).toBeInTheDocument()
    expect(row().getByRole('combobox', { name: /element type/i })).toBeInTheDocument()
  })

  it('offers the separator for a string conversion, which joins a list', () => {
    renderConfig({ targetType: 'string' })
    expect(row().getByRole('textbox', { name: /separator/i })).toBeInTheDocument()
    expect(row().queryByRole('combobox', { name: /element type/i })).not.toBeInTheDocument()
  })

  it('hides separator and element type for the other scalar types', () => {
    renderConfig({ targetType: 'int' })
    expect(row().queryByRole('textbox', { name: /separator/i })).not.toBeInTheDocument()
    expect(row().queryByRole('combobox', { name: /element type/i })).not.toBeInTheDocument()
  })

  it('writes the separator to config', async () => {
    const updateNodeConfig = vi.fn()
    const user = userEvent.setup()
    renderConfig({ targetType: 'array' }, updateNodeConfig)
    await user.type(row().getByRole('textbox', { name: /separator/i }), '|')
    expect(writtenRow(updateNodeConfig).separator).toBe('|')
  })

  it('writes the element type to config', async () => {
    const updateNodeConfig = vi.fn()
    const user = userEvent.setup()
    renderConfig({ targetType: 'array' }, updateNodeConfig)
    const dropdown = await openDropdown(user, /element type/i)
    const uuidOption = Array.from(dropdown.querySelectorAll('[role="option"]'))
      .find((o) => o.textContent === 'UUID') as HTMLElement
    await user.click(uuidOption)
    expect(writtenRow(updateNodeConfig).elementType).toBe('uuid')
  })

  // Every element type the control offers must be one convertScalar accepts.
  it('offers only element types the backend can coerce', async () => {
    const user = userEvent.setup()
    renderConfig({ targetType: 'array' })
    const dropdown = await openDropdown(user, /element type/i)
    expect(optionLabels(dropdown)).toEqual([
      'Leave as-is', 'String', 'Integer', 'Float', 'Boolean', 'UUID',
    ])
  })
})
