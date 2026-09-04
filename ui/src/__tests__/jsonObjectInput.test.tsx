import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { MantineProvider } from '@mantine/core'
import { JsonObjectInput } from '@/components/common/JsonObjectInput'

const ui = (node: React.ReactNode) => render(<MantineProvider>{node}</MantineProvider>)

/** Mirrors how the transformation form uses it: upstream owns the object. */
function Harness({ initial = {}, onCommit }: { initial?: Record<string, unknown>; onCommit?: (v: any) => void }) {
  const [value, setValue] = useState<Record<string, unknown>>(initial)
  return (
    <>
      <JsonObjectInput
        label="Fields (JSON)"
        value={value}
        onChange={(next: Record<string, unknown>) => { setValue(next); onCommit?.(next) }}
      />
      <div data-testid="upstream">{JSON.stringify(value)}</div>
    </>
  )
}

describe('JsonObjectInput', () => {
  /**
   * The raw-JSON editors derived their `value` by re-serialising the node config
   * on every render, and swallowed parse failures in an empty catch. Half-typed
   * JSON is invalid JSON, so nothing committed and nothing explained why — and
   * the next unrelated re-render replaced whatever had been typed with the old
   * serialised config.
   */
  it('keeps half-typed JSON on screen instead of reverting it', async () => {
    const user = userEvent.setup()
    ui(<Harness initial={{ 'column.a': 1 }} />)

    const box = screen.getByLabelText('Fields (JSON)')
    await user.clear(box)
    await user.type(box, '{{"column.b"')

    expect(box).toHaveValue('{"column.b"')
  })

  it('explains why an invalid document is not being applied', async () => {
    const user = userEvent.setup()
    ui(<Harness initial={{ 'column.a': 1 }} />)

    const box = screen.getByLabelText('Fields (JSON)')
    await user.clear(box)
    await user.type(box, '{{ nope')

    await waitFor(() => expect(screen.getByText(/not valid json/i)).toBeInTheDocument())
  })

  it('commits once the document parses', async () => {
    const onCommit = vi.fn()
    const user = userEvent.setup()
    ui(<Harness initial={{}} onCommit={onCommit} />)

    const box = screen.getByLabelText('Fields (JSON)')
    await user.clear(box)
    await user.type(box, '{{"column.b": 2}')

    await waitFor(() => expect(screen.getByTestId('upstream')).toHaveTextContent('{"column.b":2}'))
    expect(onCommit).toHaveBeenLastCalledWith({ 'column.b': 2 })
  })

  /**
   * Committing must not hand the user's text back to them reformatted — that is
   * what moved the caret to the end on every keystroke.
   */
  it('does not reformat the draft while the user is still typing', async () => {
    const user = userEvent.setup()
    ui(<Harness initial={{}} />)

    const box = screen.getByLabelText('Fields (JSON)')
    await user.clear(box)
    await user.type(box, '{{"a":1}')

    // Valid, so it committed — but the compact text the user typed is untouched.
    await waitFor(() => expect(screen.getByTestId('upstream')).toHaveTextContent('{"a":1}'))
    expect(box).toHaveValue('{"a":1}')
  })

  it('rejects a JSON document that is not an object', async () => {
    const user = userEvent.setup()
    ui(<Harness initial={{}} />)

    const box = screen.getByLabelText('Fields (JSON)')
    await user.clear(box)
    // `[` opens a key descriptor in user-event, so it is escaped as `[[`.
    await user.type(box, '[[1, 2]')

    await waitFor(() => expect(screen.getByText(/must be a json object/i)).toBeInTheDocument())
    expect(screen.getByTestId('upstream')).toHaveTextContent('{}')
  })

  it('adopts a change that came from elsewhere', async () => {
    function External() {
      const [value, setValue] = useState<Record<string, unknown>>({ 'column.a': 1 })
      return (
        <>
          <button onClick={() => setValue({ 'column.z': 9 })}>external edit</button>
          <JsonObjectInput label="Fields (JSON)" value={value} onChange={setValue} />
        </>
      )
    }
    const user = userEvent.setup()
    ui(<External />)

    await user.click(screen.getByRole('button', { name: 'external edit' }))
    await waitFor(() =>
      expect(screen.getByLabelText('Fields (JSON)')).toHaveValue(JSON.stringify({ 'column.z': 9 }, null, 2))
    )
  })
})
