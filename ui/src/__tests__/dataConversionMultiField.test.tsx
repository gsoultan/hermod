import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { vi } from 'vitest'
import { DataConversionConfig } from '@/components/workflow/Transformation/configs/data/DataConversionConfig'

// A data_conversion node holds a list of rows, each converting one field to its
// own target type. It used to hold exactly one field and one target type, so a
// row with five columns to retype meant five chained nodes.
//
// The keys written here are the keys parseConversions reads in
// pkg/comm/transformer/core/conversion.go. A rename on either side is a node
// that silently stops converting, which is the failure the backend cannot see
// and the operator cannot either.
describe('data_conversion multiple fields', () => {
  const renderConfig = (config: any, updateNodeConfig = vi.fn()) => {
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
    return updateNodeConfig
  }

  const row = (i: number) => within(screen.getByTestId(`conversion-row-${i}`))

  const lastPatch = (fn: ReturnType<typeof vi.fn>) => fn.mock.calls.at(-1)?.[1]

  it('renders one editor per configured conversion', () => {
    renderConfig({
      conversions: [
        { field: 'amount', targetType: 'float' },
        { field: 'qty', targetType: 'int' },
        { field: 'tags', targetType: 'array' },
      ],
    })
    expect(screen.getAllByTestId(/^conversion-row-/)).toHaveLength(3)
    expect(row(0).getByRole('combobox', { name: 'Field' })).toHaveValue('amount')
    expect(row(1).getByRole('combobox', { name: 'Field' })).toHaveValue('qty')
    expect(row(2).getByRole('combobox', { name: 'Field' })).toHaveValue('tags')
  })

  // Each row carries its own target type, which is the whole point of the list.
  it('keeps each row on its own target type', () => {
    renderConfig({
      conversions: [
        { field: 'amount', targetType: 'float' },
        { field: 'qty', targetType: 'int' },
      ],
    })
    expect(row(0).getByRole('combobox', { name: /target type/i })).toHaveValue('Float')
    expect(row(1).getByRole('combobox', { name: /target type/i })).toHaveValue('Integer')
  })

  // Editing one row must not disturb the others. Rebuilding the list from the
  // edited row alone is the obvious way to get this wrong, and it would drop
  // every other conversion on the node without an error anywhere.
  it('edits one row without touching the rest', async () => {
    const user = userEvent.setup()
    const updateNodeConfig = renderConfig({
      conversions: [
        { field: 'amount', targetType: 'float' },
        { field: 'qty', targetType: 'int' },
      ],
    })
    await user.type(row(1).getByRole('textbox', { name: /target field/i }), 'q')
    const written = lastPatch(updateNodeConfig).conversions
    expect(written).toHaveLength(2)
    expect(written[0]).toEqual({ field: 'amount', targetType: 'float' })
    expect(written[1].targetField).toBe('q')
    expect(written[1].targetType).toBe('int')
  })

  it('appends an empty row on Add Conversion', async () => {
    const user = userEvent.setup()
    const updateNodeConfig = renderConfig({ conversions: [{ field: 'amount', targetType: 'float' }] })
    await user.click(screen.getByRole('button', { name: /add conversion/i }))
    const written = lastPatch(updateNodeConfig).conversions
    expect(written).toHaveLength(2)
    expect(written[1].field).toBe('')
  })

  it('removes the row whose button was clicked', async () => {
    const user = userEvent.setup()
    const updateNodeConfig = renderConfig({
      conversions: [
        { field: 'amount', targetType: 'float' },
        { field: 'qty', targetType: 'int' },
        { field: 'tags', targetType: 'array' },
      ],
    })
    await user.click(screen.getByRole('button', { name: /remove conversion 2/i }))
    expect(lastPatch(updateNodeConfig).conversions).toEqual([
      { field: 'amount', targetType: 'float' },
      { field: 'tags', targetType: 'array' },
    ])
  })

  // Deleting the last row leaves an empty list, and the backend reads an empty
  // list as "convert nothing" rather than falling back to the pre-list keys.
  it('writes an empty list when the last row is removed', async () => {
    const user = userEvent.setup()
    const updateNodeConfig = renderConfig({ conversions: [{ field: 'amount', targetType: 'float' }] })
    await user.click(screen.getByRole('button', { name: /remove conversion 1/i }))
    expect(lastPatch(updateNodeConfig).conversions).toEqual([])
  })

  describe('per-row On Error', () => {
    it('defaults a row to the node setting', () => {
      renderConfig({ errorBehavior: 'null', conversions: [{ field: 'amount', targetType: 'float' }] })
      expect(row(0).getByRole('combobox', { name: /on error/i })).toHaveValue('Use node default')
      expect(screen.getByRole('combobox', { name: /on error \(default\)/i })).toHaveValue('Set to NULL')
    })

    it('writes a row override', async () => {
      const user = userEvent.setup()
      const updateNodeConfig = renderConfig({
        errorBehavior: 'fail',
        conversions: [{ field: 'amount', targetType: 'float' }],
      })
      const select = row(0).getByRole('combobox', { name: /on error/i })
      await user.click(select)
      const dropdown = document.getElementById(select.getAttribute('aria-controls') || '') as HTMLElement
      const option = Array.from(dropdown.querySelectorAll('[role="option"]'))
        .find((o) => o.textContent === 'Set to NULL') as HTMLElement
      await user.click(option)
      expect(lastPatch(updateNodeConfig).conversions[0].errorBehavior).toBe('null')
    })

    // Clearing an override has to write an empty behaviour, not the literal
    // sentinel: parseConversions treats an empty string as "inherit the node's"
    // and would reject "inherit" as an unknown behaviour, silently falling back
    // to "fail".
    it('clears a row override back to inheriting', async () => {
      const user = userEvent.setup()
      const updateNodeConfig = renderConfig({
        errorBehavior: 'fail',
        conversions: [{ field: 'amount', targetType: 'float', errorBehavior: 'null' }],
      })
      const select = row(0).getByRole('combobox', { name: /on error/i })
      await user.click(select)
      const dropdown = document.getElementById(select.getAttribute('aria-controls') || '') as HTMLElement
      const option = Array.from(dropdown.querySelectorAll('[role="option"]'))
        .find((o) => o.textContent === 'Use node default') as HTMLElement
      await user.click(option)
      expect(lastPatch(updateNodeConfig).conversions[0].errorBehavior).toBe('')
    })

    it('writes the node default to the node, not to a row', async () => {
      const user = userEvent.setup()
      const updateNodeConfig = renderConfig({ conversions: [{ field: 'amount', targetType: 'float' }] })
      const select = screen.getByRole('combobox', { name: /on error \(default\)/i })
      await user.click(select)
      const dropdown = document.getElementById(select.getAttribute('aria-controls') || '') as HTMLElement
      const option = Array.from(dropdown.querySelectorAll('[role="option"]'))
        .find((o) => o.textContent === 'Keep original') as HTMLElement
      await user.click(option)
      expect(lastPatch(updateNodeConfig)).toEqual({ errorBehavior: 'keep' })
    })
  })

  describe('configs stored before the node held rows', () => {
    it('shows the stored conversion as a single row', () => {
      renderConfig({ field: 'amount', targetType: 'float', targetField: 'amount_num' })
      expect(screen.getAllByTestId(/^conversion-row-/)).toHaveLength(1)
      expect(row(0).getByRole('combobox', { name: 'Field' })).toHaveValue('amount')
      expect(row(0).getByRole('combobox', { name: /target type/i })).toHaveValue('Float')
      expect(row(0).getByRole('textbox', { name: /target field/i })).toHaveValue('amount_num')
    })

    // The first edit migrates the node, and has to clear the pre-list keys as
    // it goes. Leaving them behind would put two conversions in one config that
    // disagree, with only the reader's precedence rule deciding which one runs.
    it('migrates to a row list and clears the pre-list keys', async () => {
      const user = userEvent.setup()
      const updateNodeConfig = renderConfig({
        field: 'amount',
        targetType: 'array',
        separator: '|',
        elementType: 'int',
        targetField: 'amount_list',
        errorBehavior: 'null',
      })
      await user.click(screen.getByRole('button', { name: /add conversion/i }))

      const patch = lastPatch(updateNodeConfig)
      expect(patch.conversions[0]).toEqual({
        field: 'amount',
        targetType: 'array',
        separator: '|',
        elementType: 'int',
        targetField: 'amount_list',
      })
      for (const key of ['field', 'targetType', 'format', 'separator', 'elementType', 'targetField']) {
        expect(patch[key]).toBeUndefined()
        expect(key in patch).toBe(true)
      }
      // The node-level behaviour is not a pre-list key -- it is the default the
      // rows inherit -- so the migration must leave it alone.
      expect('errorBehavior' in patch).toBe(false)
    })

    it('starts a brand-new node with one empty row', () => {
      renderConfig({})
      expect(screen.getAllByTestId(/^conversion-row-/)).toHaveLength(1)
      expect(row(0).getByRole('combobox', { name: 'Field' })).toHaveValue('')
    })
  })
})
