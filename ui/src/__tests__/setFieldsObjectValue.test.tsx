import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { useEffect, useState } from 'react'
import { SetFieldEditor } from '@/components/workflow/Transformation/fieldMappings/SetFieldEditor'

/**
 * A set field's value can be a JSON object: `column.after.QueryParams` holding
 * `{"session": "source.after.session.sessions.0.access_token"}` is how an API
 * lookup's query parameters are built. The row editor only knew text, so it
 * showed such a value as `String(value)` -- "[object Object]" -- and typing
 * into it replaced the object with that text.
 */

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
        addField={() => {}}
      />
    </MantineProvider>
  )
}

const renderEditor = (initial: Record<string, unknown>) => {
  let latest = initial
  const onData = (data: Record<string, unknown>) => {
    latest = data
  }
  render(<Harness initial={initial} onData={onData} />)
  return () => latest
}

const valueType = () => screen.getByRole('radiogroup', { name: /value type/i })

describe('a set field whose value is JSON', () => {
  it('shows the value as JSON rather than [object Object]', () => {
    renderEditor({
      'column.after.QueryParams': { session: 'source.after.session.sessions.0.access_token' },
    })

    const box = screen.getByRole('textbox', { name: /json value/i })
    expect(JSON.parse((box as HTMLTextAreaElement).value)).toEqual({
      session: 'source.after.session.sessions.0.access_token',
    })
    expect(screen.queryByDisplayValue('[object Object]')).toBeNull()
  })

  it('commits an edited JSON value as an object, not as text', async () => {
    const user = userEvent.setup()
    const config = renderEditor({ 'column.after.QueryParams': { session: 'source.a' } })

    const box = screen.getByRole('textbox', { name: /json value/i })
    await user.clear(box)
    await user.type(box, '{{"session": "source.b", "page": 2}')

    await waitFor(() =>
      expect(config()['column.after.QueryParams']).toEqual({ session: 'source.b', page: 2 }),
    )
  })

  it('turns a row into JSON and back without losing its value', async () => {
    const user = userEvent.setup()
    const config = renderEditor({ 'column.q': '{"a": 1}' })

    await user.click(within(valueType()).getByRole('radio', { name: 'JSON' }))
    expect(config()['column.q']).toEqual({ a: 1 })

    await user.click(within(valueType()).getByRole('radio', { name: 'Expression' }))
    expect(config()['column.q']).toBe('{"a":1}')
  })

  it('keeps an expression that is not JSON inside the new object', async () => {
    const user = userEvent.setup()
    const config = renderEditor({ 'column.q': 'source.name' })

    await user.click(within(valueType()).getByRole('radio', { name: 'JSON' }))

    expect(config()['column.q']).toEqual({ value: 'source.name' })
  })

  // A raw-JSON edit can store a number or a boolean. `String(value || '')`
  // showed 0 and false as an empty box, and the next keystroke replaced them.
  it('shows a number or boolean value as it is stored', () => {
    renderEditor({ 'column.limit': 0, 'column.enabled': false })

    const values = screen.getAllByRole('textbox', { name: /value or expression/i })
    expect(values.map((v) => (v as HTMLInputElement).value)).toEqual(['0', 'false'])
  })

  it('offers an array as a JSON value too', async () => {
    const user = userEvent.setup()
    const config = renderEditor({ 'column.ids': ['source.a', 'source.b'] })

    const box = screen.getByRole('textbox', { name: /json value/i })
    await user.clear(box)
    await user.type(box, '[["source.c"]')

    await waitFor(() => expect(config()['column.ids']).toEqual(['source.c']))
  })
})
