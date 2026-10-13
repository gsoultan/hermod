import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { useState } from 'react'
import { SecretNamesContext } from '@/components/shared/TemplateField'
import { PasswordInput, TextInput } from '@/components/shared/ReferenceInputs'

/**
 * Every text and password input of a source, sink or transformation form
 * offers the vhost's secrets and a server environment variable, inserted as
 * the {{ }} token the engine resolves -- the same way in each of them, so a
 * DSN, a header or a key never has to be typed in the clear.
 */

function Controlled({ password = false, initial = '' }: { password?: boolean; initial?: string }) {
  const [value, setValue] = useState(initial)
  const Input = password ? PasswordInput : TextInput
  return <Input label="Password" value={value} onChange={(e) => setValue(e.currentTarget.value)} />
}

const renderWith = (ui: React.ReactNode, names = ['DB_PASS', 'API_KEY']) =>
  render(
    <MantineProvider>
      <SecretNamesContext.Provider value={names}>{ui}</SecretNamesContext.Provider>
    </MantineProvider>,
  )

const openMenu = async (user: ReturnType<typeof userEvent.setup>) => {
  await user.click(screen.getByRole('button', { name: /insert a secret or environment variable/i }))
  return screen.findByRole('dialog', { hidden: true }, { timeout: 5000 })
}

describe('secret and env references in config inputs', () => {
  it('inserts a secret into a controlled text input', async () => {
    const user = userEvent.setup()
    renderWith(<Controlled />)
    const menu = await openMenu(user)
    await user.click(within(menu).getByRole('button', { name: 'DB_PASS', hidden: true }))
    expect(screen.getByRole('textbox', { name: 'Password' })).toHaveValue('{{secret("DB_PASS")}}')
  })

  it('inserts at the cursor, keeping what is around it', async () => {
    const user = userEvent.setup()
    renderWith(<Controlled initial="postgres://app:@db" />)
    const input = screen.getByRole('textbox', { name: 'Password' }) as HTMLInputElement
    input.focus()
    input.setSelectionRange(15, 15)
    const menu = await openMenu(user)
    await user.click(within(menu).getByRole('button', { name: 'API_KEY', hidden: true }))
    expect(input).toHaveValue('postgres://app:{{secret("API_KEY")}}@db')
  })

  it('inserts an environment variable by name', async () => {
    const user = userEvent.setup()
    renderWith(<Controlled />, [])
    const menu = await openMenu(user)
    expect(within(menu).getByText(/no secrets/i)).toBeInTheDocument()
    await user.type(within(menu).getByRole('textbox', { name: /environment variable/i, hidden: true }), 'PG_HOST')
    await user.click(within(menu).getByRole('button', { name: /insert variable/i, hidden: true }))
    expect(screen.getByRole('textbox', { name: 'Password' })).toHaveValue('{{env("PG_HOST")}}')
  })

  it('refuses an environment variable name the server would not read', async () => {
    const user = userEvent.setup()
    renderWith(<Controlled />, [])
    const menu = await openMenu(user)
    await user.type(within(menu).getByRole('textbox', { name: /environment variable/i, hidden: true }), 'bad-name')
    expect(within(menu).getByRole('button', { name: /insert variable/i, hidden: true })).toBeDisabled()
  })

  it('works in a password input', async () => {
    const user = userEvent.setup()
    renderWith(<Controlled password />)
    const menu = await openMenu(user)
    await user.click(within(menu).getByRole('button', { name: 'DB_PASS', hidden: true }))
    expect(screen.getByLabelText('Password')).toHaveValue('{{secret("DB_PASS")}}')
  })

  it('offers nothing on a read-only input', () => {
    renderWith(<TextInput label="Password" value="x" readOnly />)
    expect(screen.queryByRole('button', { name: /insert a secret/i })).not.toBeInTheDocument()
  })
})
