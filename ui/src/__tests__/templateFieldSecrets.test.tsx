import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { useState } from 'react'
import { SecretNamesContext, TemplateField } from '@/components/shared/TemplateField'

/**
 * Where the editor offers "Insert variable", it also offers the secrets the
 * workflow's vhost holds. Nobody should have to remember, or retype, the exact
 * name they saved on another page -- and only names are listed: there is no
 * value in the browser to show.
 */

function Field({ buildSecretToken }: { buildSecretToken?: (name: string) => string }) {
  const [value, setValue] = useState('')
  return (
    <TemplateField
      aria-label="Value or expression"
      value={value}
      onChange={setValue}
      availableFields={['after.id']}
      buildSecretToken={buildSecretToken}
    />
  )
}

const renderField = (names: string[] | undefined, buildSecretToken?: (name: string) => string) =>
  render(
    <MantineProvider>
      {names ? (
        <SecretNamesContext.Provider value={names}>
          <Field buildSecretToken={buildSecretToken} />
        </SecretNamesContext.Provider>
      ) : (
        <Field buildSecretToken={buildSecretToken} />
      )}
    </MantineProvider>,
  )

const openPicker = async (user: ReturnType<typeof userEvent.setup>) => {
  await user.click(screen.getByRole('button', { name: /insert variable/i }))
  return screen.findByRole('dialog', { hidden: true }, { timeout: 5000 })
}

describe('secret names in the variable picker', () => {
  it('lists the vhost’s secrets and inserts one as a template token', async () => {
    const user = userEvent.setup()
    renderField(['API_KEY', 'DB_PASS'])

    const picker = await openPicker(user)
    const secrets = within(picker).getByRole('group', { name: /secrets/i, hidden: true })
    expect(within(secrets).getByText('DB_PASS')).toBeInTheDocument()
    await user.click(within(secrets).getByText('API_KEY'))

    expect(screen.getByRole('textbox', { name: /value or expression/i })).toHaveValue('{{secret("API_KEY")}}')
  })

  it('inserts the spelling the field asks for', async () => {
    const user = userEvent.setup()
    renderField(['API_KEY'], (name) => `secret("${name}")`)

    const picker = await openPicker(user)
    await user.click(within(within(picker).getByRole('group', { name: /secrets/i, hidden: true })).getByText('API_KEY'))

    expect(screen.getByRole('textbox', { name: /value or expression/i })).toHaveValue('secret("API_KEY")')
  })

  it('narrows the secrets with the same search as the fields', async () => {
    const user = userEvent.setup()
    renderField(['API_KEY', 'DB_PASS'])

    const picker = await openPicker(user)
    await user.type(within(picker).getByPlaceholderText(/search/i), 'db')

    const secrets = within(picker).getByRole('group', { name: /secrets/i, hidden: true })
    expect(within(secrets).getByText('DB_PASS')).toBeInTheDocument()
    expect(within(secrets).queryByText('API_KEY')).toBeNull()
  })

  it('shows no secrets section when the vhost has none', async () => {
    const user = userEvent.setup()
    renderField(undefined)

    const picker = await openPicker(user)
    expect(within(picker).queryByRole('group', { name: /secrets/i, hidden: true })).toBeNull()
  })
})
