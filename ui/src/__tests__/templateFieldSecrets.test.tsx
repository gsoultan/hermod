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

describe('the nested builder inside a value input', () => {
  function Built() {
    const [value, setValue] = useState('{{.after.id}}')
    return (
      <SecretNamesContext.Provider value={['API_KEY']}>
        <TemplateField
          label="Body"
          value={value}
          onChange={setValue}
          availableFields={['after.id']}
          functions="token"
        />
      </SecretNamesContext.Provider>
    )
  }

  it('opens from inside the input and writes a nested call back into it', async () => {
    const user = userEvent.setup()
    render(
      <MantineProvider>
        <Built />
      </MantineProvider>,
    )

    await user.click(screen.getByRole('button', { name: /build expression/i }))
    const type = await screen.findByRole('combobox', { name: 'Body type' }, { timeout: 5000 })
    await user.click(type)
    await user.click(await screen.findByRole('option', { name: 'Function', hidden: true }))
    const fn = screen.getByRole('combobox', { name: 'Body function' })
    await user.click(fn)
    await user.click(await screen.findByRole('option', { name: /^upper/, hidden: true }))

    expect(screen.getByRole('textbox', { name: 'Body' })).toHaveValue('{{upper(source.after.id)}}')
  })

  // Tab from the value reaches Insert function, then Insert variable: the
  // builder comes after both, so the keyboard path to a field stays two Tabs.
  it('sits after the insert buttons in the tab order', () => {
    render(
      <MantineProvider>
        <Built />
      </MantineProvider>,
    )
    const names = screen.getAllByRole('button').map((b) => b.getAttribute('aria-label'))
    expect(names.filter((n) => /insert function|insert variable|build expression/i.test(n ?? ''))).toEqual([
      'Insert function',
      'Insert variable',
      'Build expression',
    ])
  })

  it('keeps the click-a-field tip in the variable list', async () => {
    const user = userEvent.setup()
    render(
      <MantineProvider>
        <Built />
      </MantineProvider>,
    )
    const picker = await openPicker(user)
    expect(within(picker).getByText(/^Tip: Click a field/)).toBeInTheDocument()
  })

  it('is not offered where the value cannot hold a function', () => {
    render(
      <MantineProvider>
        <TemplateField label="Name" value="" onChange={() => {}} />
      </MantineProvider>,
    )
    expect(screen.queryByRole('button', { name: /build expression/i })).toBeNull()
  })
})

describe('a credential input', () => {
  function Credential({ initial }: { initial: string }) {
    const [value, setValue] = useState(initial)
    return <TemplateField label="Token" value={value} onChange={setValue} masked secretsOnly />
  }

  const renderCredential = (initial: string) =>
    render(
      <MantineProvider>
        <SecretNamesContext.Provider value={['API_TOKEN']}>
          <Credential initial={initial} />
        </SecretNamesContext.Provider>
      </MantineProvider>,
    )

  it('hides a typed-in value until it is revealed', async () => {
    const user = userEvent.setup()
    renderCredential('abc123')
    expect(screen.getByLabelText('Token')).toHaveAttribute('type', 'password')
    await user.click(screen.getByRole('button', { name: 'Show value' }))
    expect(screen.getByLabelText('Token')).toHaveAttribute('type', 'text')
  })

  it('shows a secret reference as written, since it is only a name', () => {
    renderCredential('{{secret("API_TOKEN")}}')
    expect(screen.getByLabelText('Token')).toHaveAttribute('type', 'text')
  })

  it('offers only secrets, never record fields', async () => {
    const user = userEvent.setup()
    renderCredential('')
    await user.click(screen.getByRole('button', { name: 'Insert secret' }))
    const picker = await screen.findByRole('dialog', { hidden: true }, { timeout: 5000 })
    await user.click(within(picker).getByText('API_TOKEN'))
    expect(screen.getByLabelText('Token')).toHaveValue('{{secret("API_TOKEN")}}')
  })
})

describe('a value whose tokens are bound as SQL parameters', () => {
  it('offers fields but no secrets, which would bind NULL there', async () => {
    const user = userEvent.setup()
    render(
      <MantineProvider>
        <SecretNamesContext.Provider value={['API_KEY']}>
          <TemplateField aria-label="Query" value="" onChange={() => {}} availableFields={['after.id']} noSecrets />
        </SecretNamesContext.Provider>
      </MantineProvider>,
    )
    await user.click(screen.getByRole('button', { name: /insert variable/i }))
    const picker = await screen.findByRole('dialog', { hidden: true }, { timeout: 5000 })
    expect(within(picker).getByText('after.id')).toBeInTheDocument()
    expect(within(picker).queryByRole('group', { name: /secrets/i, hidden: true })).toBeNull()
  })
})
