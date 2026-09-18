import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { useEffect, useState } from 'react'
import { SetFieldEditor } from '@/components/workflow/Transformation/SetFieldEditor'
import {
  listColumnFields,
  nextColumnFieldName,
  renameColumnField,
} from '@/components/workflow/Transformation/columnFields'

/**
 * A `set` (or `advanced`) node stores its field mappings as flat `column.<path>`
 * keys on the node config, so the editor's row order is nothing but JavaScript
 * object key order.
 *
 * Renaming used to rebuild that object as
 * `{ ...base, ...otherColumns, ['column.' + newPath]: value }`, which drops the
 * edited key and re-appends it last. The Target Path input fires on every
 * keystroke, so typing one character into the first of three rows sent that row
 * to the bottom of the list — and with rows keyed by index, the caret was then
 * sitting in a different row's input.
 */

const TARGET_PATH = 'e.g. user.id'

const pathValues = () =>
  screen.getAllByPlaceholderText(TARGET_PATH).map((i) => (i as HTMLInputElement).value)

function Harness({
  initial,
  onData,
}: {
  initial: Record<string, unknown>
  onData: (data: Record<string, unknown>) => void
}) {
  const [data, setData] = useState<Record<string, unknown>>(initial)
  useEffect(() => {
    onData(data)
  }, [data, onData])
  // Mirrors useWorkflowStore.updateNodeConfig: merge by default, replace on flag.
  const updateNodeConfig = (_id: string, config: any, replace = false) =>
    setData((prev) => (replace ? config : { ...prev, ...config }))

  return (
    <MantineProvider>
      <SetFieldEditor
        selectedNode={{ id: 'n1', data }}
        updateNodeConfig={updateNodeConfig}
        availableFields={[]}
        transType="set"
        onAddFromSource={() => {}}
        addField={() => {}}
      />
    </MantineProvider>
  )
}

/** Renders the editor and returns a reader for the config as it stands now. */
const renderEditor = (initial: Record<string, unknown>) => {
  let latest = initial
  // Defined once so the harness effect fires on data changes, not every render.
  const onData = (data: Record<string, unknown>) => {
    latest = data
  }
  render(<Harness initial={initial} onData={onData} />)
  return () => latest
}

const threeRows = {
  label: 'Set fields',
  transType: 'set',
  'column.alpha': '1',
  'column.beta': '2',
  'column.gamma': '3',
}

describe('set/advanced field rows keep their order', () => {
  it('does not move a row while the user types in its target path', async () => {
    const user = userEvent.setup()
    renderEditor(threeRows)
    expect(pathValues()).toEqual(['alpha', 'beta', 'gamma'])

    await user.type(screen.getAllByPlaceholderText(TARGET_PATH)[0], '_id')

    expect(pathValues()).toEqual(['alpha_id', 'beta', 'gamma'])
  })

  it('leaves the caret in the row being edited', async () => {
    const user = userEvent.setup()
    renderEditor(threeRows)

    const middle = screen.getAllByPlaceholderText(TARGET_PATH)[1]
    await user.type(middle, '_id')

    expect(document.activeElement).toBe(screen.getAllByPlaceholderText(TARGET_PATH)[1])
    expect((document.activeElement as HTMLInputElement).value).toBe('beta_id')
  })

  it('keeps the values attached to their own rows', async () => {
    const user = userEvent.setup()
    const config = renderEditor(threeRows)

    await user.type(screen.getAllByPlaceholderText(TARGET_PATH)[0], '_id')

    expect(config()['column.alpha_id']).toBe('1')
    expect(config()['column.beta']).toBe('2')
    expect(config()['column.gamma']).toBe('3')
    expect(config()['column.alpha']).toBeUndefined()
    // Non-column keys survive the replace.
    expect(config().label).toBe('Set fields')
  })

  /**
   * Renaming onto a path another row already holds collapsed two rows into one:
   * the spread wrote the edited row's value over the existing key, and the row
   * count silently dropped. The typed text is kept in the input, the rename is
   * not committed, and the collision is named.
   */
  it('refuses a rename that collides with another row instead of merging them', async () => {
    const user = userEvent.setup()
    const config = renderEditor({
      transType: 'set',
      'column.alpha': '1',
      'column.beta': '2',
    })

    const second = screen.getAllByPlaceholderText(TARGET_PATH)[1]
    await user.clear(second)
    await user.type(second, 'alpha')

    expect(pathValues()).toEqual(['alpha', 'alpha'])
    expect(screen.getByText(/already/i)).toBeInTheDocument()
    // The row that was already there is untouched, and nothing was lost.
    expect(config()['column.alpha']).toBe('1')
    expect(Object.values(config())).toContain('2')
  })

  it('commits the rename once the collision is typed away', async () => {
    const user = userEvent.setup()
    const config = renderEditor({
      transType: 'set',
      'column.alpha': '1',
      'column.beta': '2',
    })

    const second = screen.getAllByPlaceholderText(TARGET_PATH)[1]
    await user.clear(second)
    await user.type(second, 'alpha')
    await user.type(second, '_2')

    expect(pathValues()).toEqual(['alpha', 'alpha_2'])
    expect(screen.queryByText(/already/i)).not.toBeInTheDocument()
    expect(config()['column.alpha']).toBe('1')
    expect(config()['column.alpha_2']).toBe('2')
  })

  /**
   * A draft is held only because the path collided. Remove the row that owned
   * that path and the collision is over — the error must not outlive it, and the
   * text left in the input must not be stranded uncommitted.
   */
  it('commits a held rename once the row it collided with is gone', async () => {
    const user = userEvent.setup()
    const config = renderEditor({
      transType: 'set',
      'column.alpha': '1',
      'column.beta': '2',
    })

    const second = screen.getAllByPlaceholderText(TARGET_PATH)[1]
    await user.clear(second)
    await user.type(second, 'alpha')
    expect(screen.getByText(/already/i)).toBeInTheDocument()

    await user.click(screen.getAllByRole('button', { name: /remove field/i })[0])

    expect(screen.queryByText(/already/i)).not.toBeInTheDocument()
    expect(pathValues()).toEqual(['alpha'])
    expect(config()['column.alpha']).toBe('2')
  })

  it('removes the row the delete button belongs to', async () => {
    const user = userEvent.setup()
    const config = renderEditor(threeRows)

    await user.click(screen.getAllByRole('button', { name: /remove field/i })[1])

    expect(pathValues()).toEqual(['alpha', 'gamma'])
    expect(config()['column.beta']).toBeUndefined()
  })

  it('labels each input so the row is reachable without sighted ordering', () => {
    renderEditor(threeRows)
    expect(screen.getAllByRole('combobox', { name: /target path/i })).toHaveLength(3)
    expect(screen.getAllByRole('textbox', { name: /value or expression/i })).toHaveLength(3)
  })
})

describe('columnFields helpers', () => {
  it('renames in place rather than re-appending', () => {
    const next = renameColumnField(
      { label: 'n', 'column.a': 1, 'column.b': 2, 'column.c': 3 },
      'column.a',
      'z',
    )
    expect(next).not.toBeNull()
    expect(Object.keys(next!)).toEqual(['label', 'column.z', 'column.b', 'column.c'])
  })

  it('reports a collision instead of dropping a row', () => {
    expect(renameColumnField({ 'column.a': 1, 'column.b': 2 }, 'column.b', 'a')).toBeNull()
  })

  it('treats a rename to the same path as a no-op', () => {
    expect(renameColumnField({ 'column.a': 1 }, 'column.a', 'a')).toBeNull()
  })

  it('lists only column keys, in config order', () => {
    expect(
      listColumnFields({ label: 'n', 'column.b': 2, other: 1, 'column.a': 1 }),
    ).toEqual([
      { fullKey: 'column.b', path: 'b', value: 2 },
      { fullKey: 'column.a', path: 'a', value: 1 },
    ])
  })

  /**
   * The generated name used to be `new_field_${count}`. Delete a middle row and
   * the count points at a name that is still taken, so "Add New Field Mapping"
   * overwrote an existing row and appeared to do nothing.
   */
  it('never generates a field name that is already taken', () => {
    expect(
      nextColumnFieldName({ 'column.new_field_0': '', 'column.new_field_2': '' }),
    ).toBe('new_field_1')
    expect(
      nextColumnFieldName({ 'column.new_field_0': '', 'column.new_field_1': '' }),
    ).toBe('new_field_2')
    expect(nextColumnFieldName({})).toBe('new_field_0')
  })
})
