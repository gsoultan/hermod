import { render, screen, fireEvent } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { vi } from 'vitest'
import { OtherSourceConfig } from '@/components/workflow/Source/OtherSourceConfig'

/**
 * The credentials a source form offers have to be ones its endpoint checks.
 *
 * The webhook form offered an API key the endpoint never read, and did not
 * offer the one credential the endpoint did check — the signing secret, which
 * could only be set through the API. The form source showed the same API key
 * field with the same promise, and its endpoint, which answers browsers,
 * checks no key at all.
 *
 * The keys written are the ones the endpoint reads: `api_key` and `secret`
 * (internal/webhooks/transport/http, authenticateWebhook).
 */
function renderConfig(sourceType: string, config: Record<string, any>) {
  const updateConfig = vi.fn()
  render(
    <MantineProvider>
      <OtherSourceConfig config={config} updateConfig={updateConfig} sourceType={sourceType} />
    </MantineProvider>,
  )
  return updateConfig
}

describe('the credentials on a webhook source', () => {
  it('offers the API key and says which header carries it', () => {
    renderConfig('webhook', { path: '/api/webhooks/x' })
    expect(screen.getByText(/API Key \(Optional\)/)).toBeInTheDocument()
    expect(screen.getByText('X-API-Key')).toBeInTheDocument()
  })

  it('offers the signing secret, and writes it where the endpoint reads it', () => {
    const updateConfig = renderConfig('webhook', { path: '/api/webhooks/x' })
    const secret = screen.getByLabelText(/Signing secret/i)
    fireEvent.change(secret, { target: { value: 's3cret' } })
    expect(updateConfig).toHaveBeenCalledWith('secret', 's3cret')
  })

  it('names the header a signature travels in', () => {
    renderConfig('webhook', { path: '/api/webhooks/x' })
    expect(screen.getByText('X-Hub-Signature-256')).toBeInTheDocument()
  })
})

describe('the credentials on a form source', () => {
  it('does not offer an API key its endpoint does not check', () => {
    renderConfig('form', { path: '/api/forms/x' })
    expect(screen.queryByText(/API Key \(Optional\)/)).not.toBeInTheDocument()
    expect(screen.queryByLabelText(/Signing secret/i)).not.toBeInTheDocument()
  })
})
