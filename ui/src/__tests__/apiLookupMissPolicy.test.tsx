import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { vi } from 'vitest'
import { APILookupConfig } from '@/components/workflow/Transformation/configs/enrichment/APILookupConfig'

// api_lookup gained the same miss policy db_lookup has
// (pkg/comm/transformer/lookup/onmiss.go), and the same reasoning applies: a
// lookup that enriched nothing must not reach the sink looking like one that
// did. The editor has to offer the choice, and it has to mirror the backend's
// inference — with the one difference that matters here, which is that a failed
// *request* defaults to failing rather than to passing through
// (apiErrorPolicy).
describe('api_lookup miss policy and cache TTL', () => {
  const renderSettings = async (config: any, updateNodeConfig = () => {}) => {
    const user = userEvent.setup()
    render(
      <MantineProvider>
        <APILookupConfig
          config={config}
          updateNodeConfig={updateNodeConfig}
          nodeId="n1"
          testLookup={() => {}}
          testing={false}
        />
      </MantineProvider>
    )
    await user.click(screen.getByRole('tab', { name: /auth\/retry/i }))
    return user
  }

  const policyInput = () =>
    screen.getByRole('combobox', { name: /when the lookup returns nothing/i }) as HTMLInputElement

  it('shows passthrough when neither onMiss nor a default value is set', async () => {
    await renderSettings({ url: 'https://x' })
    expect(policyInput().value).toMatch(/pass the message through/i)
  })

  it('infers the default-value policy the same way the backend does', async () => {
    await renderSettings({ url: 'https://x', defaultValue: 'unknown' })
    expect(policyInput().value).toMatch(/default value/i)
  })

  it('writes the chosen policy to onMiss', async () => {
    const updateNodeConfig = vi.fn()
    const user = await renderSettings({ url: 'https://x' }, updateNodeConfig)

    await user.click(policyInput())
    expect(policyInput()).toHaveAttribute('aria-expanded', 'true')
    await user.click(screen.getByText('Fail the message'))

    expect(updateNodeConfig).toHaveBeenCalledWith('n1', { onMiss: 'fail' })
  })

  it('warns that the default policy writes nothing without a default value', async () => {
    await renderSettings({ url: 'https://x', onMiss: 'default', defaultValue: '' })
    expect(screen.getByTestId('api-lookup-miss-warning')).toHaveTextContent(/nothing will be written/i)
  })

  // The field used to be documented as unbounded by omission: empty meant the
  // response was kept for the lifetime of the process. Both ends of the new
  // contract have to be visible, or an operator cannot tell which one they are
  // getting.
  it('states the default TTL and how to turn the cache off', async () => {
    await renderSettings({ url: 'https://x' })
    const ttl = screen.getByTestId('api-lookup-ttl-description')
    expect(ttl).toHaveTextContent(/5m/i)
    expect(ttl).toHaveTextContent(/0/)
  })
})

// The request body's rules changed with the fix for a jsonb field arriving as
// a JSON string, and the place an operator decides how to write a token is the
// body field itself -- so that is where they are stated.
describe('APILookupConfig request body', () => {
  it('says how a token is sent and what the body is labelled', async () => {
    const user = userEvent.setup()
    render(
      <MantineProvider>
        <APILookupConfig config={{ method: 'POST', url: 'https://x' }} updateNodeConfig={() => {}} nodeId="n1" />
      </MantineProvider>
    )

    await user.click(screen.getByRole('tab', { name: /body\/headers/i }))

    expect(screen.getByText(/an object or array \(jsonb\) is sent as JSON/i)).toBeInTheDocument()
    expect(screen.getByText(/application\/json unless you set one/i)).toBeInTheDocument()
  })
})
