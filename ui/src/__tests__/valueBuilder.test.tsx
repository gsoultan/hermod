import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { useState } from 'react'
import { describe, it, expect } from 'vitest'
import { ValueBuilder } from '@/components/shared/ValueBuilder'
import type { ValueMode } from '@/lib/valueExpression'

/**
 * The builder shows a value as slots: each one text, a field, a secret, or a
 * function whose arguments are slots again -- so functions nest -- and writes
 * every change back as the one string the engine reads.
 */
function Harness({ initial, mode = 'token' }: { initial: string; mode?: ValueMode }) {
  const [value, setValue] = useState(initial)
  return (
    <>
      <ValueBuilder
        label="Value"
        value={value}
        onChange={setValue}
        mode={mode}
        fieldPaths={['email', 'first_name', 'nickname']}
        secretNames={['API_KEY']}
      />
      <output data-testid="saved">{value}</output>
    </>
  )
}

const renderBuilder = (initial: string, mode?: ValueMode) =>
  render(
    <MantineProvider>
      <Harness initial={initial} mode={mode} />
    </MantineProvider>,
  )

const saved = () => screen.getByTestId('saved').textContent

const pick = async (user: ReturnType<typeof userEvent.setup>, input: HTMLElement, name: string | RegExp) => {
  await user.click(input)
  const list = await waitFor(() => {
    const el = document.getElementById(input.getAttribute('aria-controls') ?? '')
    if (!el) throw new Error('option list not open')
    return el
  })
  await user.click(within(list).getByRole('option', { name, hidden: true }))
}

describe('ValueBuilder', () => {
  it('shows a saved field as a field slot', () => {
    renderBuilder('{{.email}}')
    expect(screen.getByRole('combobox', { name: 'Value type' })).toHaveValue('Field')
    expect(screen.getByRole('combobox', { name: 'Value field' })).toHaveValue('email')
  })

  it('wraps the current value when it becomes a function', async () => {
    const user = userEvent.setup()
    renderBuilder('{{.email}}')

    await pick(user, screen.getByRole('combobox', { name: 'Value type' }), /function/i)
    await pick(user, screen.getByRole('combobox', { name: 'Value function' }), /^lower/)

    expect(saved()).toBe('{{lower(source.email)}}')
    expect(screen.getByRole('combobox', { name: 'Value › text field' })).toHaveValue('email')
  })

  it('nests a function inside an argument', async () => {
    const user = userEvent.setup()
    renderBuilder('{{lower(source.email)}}')

    await pick(user, screen.getByRole('combobox', { name: 'Value › text type' }), /function/i)
    await pick(user, screen.getByRole('combobox', { name: 'Value › text function' }), /^trim/)

    expect(saved()).toBe('{{lower(trim(source.email))}}')
  })

  it('reads and edits a function three levels deep', async () => {
    const user = userEvent.setup()
    renderBuilder('{{upper(trim(coalesce(source.nickname, "friend")))}}')

    const fallback = screen.getByRole('textbox', { name: 'Value › text › text › b text' })
    await user.clear(fallback)
    await user.type(fallback, 'there')

    expect(saved()).toBe('{{upper(trim(coalesce(source.nickname, "there")))}}')
  })

  it('takes a secret of the vhost as an argument', async () => {
    const user = userEvent.setup()
    renderBuilder('{{lower(source.email)}}')

    await pick(user, screen.getByRole('combobox', { name: 'Value › text type' }), /secret/i)
    await pick(user, screen.getByRole('combobox', { name: 'Value › text secret' }), 'API_KEY')

    expect(saved()).toBe('{{lower(secret("API_KEY"))}}')
  })

  it('adds another argument to a function that takes any number', async () => {
    const user = userEvent.setup()
    renderBuilder('{{concat(source.first_name, " ")}}')

    await user.click(screen.getByRole('button', { name: 'Add argument to concat' }))
    await pick(user, screen.getByRole('combobox', { name: 'Value › … 3 type' }), /field/i)
    await user.type(screen.getByRole('combobox', { name: 'Value › … 3 field' }), 'nickname')

    expect(saved()).toBe('{{concat(source.first_name, " ", source.nickname)}}')
  })

  it('writes a field the way a field-or-expression input reads it', async () => {
    const user = userEvent.setup()
    renderBuilder('status', 'field')

    await pick(user, screen.getByRole('combobox', { name: 'Value type' }), /function/i)
    await pick(user, screen.getByRole('combobox', { name: 'Value function' }), /^upper/)

    expect(saved()).toBe('upper(source.status)')
  })

  it('leaves text mixed with tokens to the text input', () => {
    renderBuilder('Hello {{.first_name}}')
    expect(screen.getByText(/mixes text and tokens/i)).toBeInTheDocument()
    expect(screen.queryByRole('combobox', { name: 'Value type' })).toBeNull()
  })
})
