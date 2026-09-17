import { render, screen, waitFor } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { describe, it, expect } from 'vitest'
import { PanmailSinkConfig } from '@/components/workflow/Sink/PanmailSinkConfig'

const ui = (
  config: Record<string, string>,
  updateConfig: (key: string, value: unknown) => void = () => {},
) =>
  render(
    <MantineProvider>
      <PanmailSinkConfig config={config} updateConfig={updateConfig} />
    </MantineProvider>,
  )

const base = {
  api_key: 'key-123',
  provider_id: 'prov-1',
  from: 'noreply@example.com',
  to: '{{.email}}',
}

/**
 * `panmail.New` refuses to start when the gateway url or the api key is a
 * template and no allowlist is set. The field that satisfies it has to be
 * reachable from the form, or the only configuration the sink will accept is
 * one the UI cannot produce — the shape that made the `http` sink
 * unconfigurable (see sinkConfigCoverage.test.tsx).
 */
describe('the panmail allowlist field', () => {
  it('stays out of the way while the gateway is a fixed url', () => {
    ui({ ...base, base_url: 'https://mail.example.com' })
    expect(screen.queryByLabelText(/allowed gateway hosts/i)).not.toBeInTheDocument()
  })

  it('appears once the gateway url is templated', async () => {
    ui({ ...base, base_url: 'https://{{.tenant}}.mail.example.com' })
    await waitFor(() =>
      expect(screen.getByLabelText(/allowed gateway hosts/i)).toBeInTheDocument(),
    )
  })

  it('appears for a templated api key too', async () => {
    ui({ ...base, base_url: 'https://mail.example.com', api_key: '{{.tenant_key}}' })
    await waitFor(() =>
      expect(screen.getByLabelText(/allowed gateway hosts/i)).toBeInTheDocument(),
    )
  })

  it('writes the key the factory reads', async () => {
    const written: Array<[string, unknown]> = []
    ui(
      { ...base, base_url: 'https://{{.tenant}}.mail.example.com' },
      (key, value) => {
        written.push([key, value])
      },
    )

    const field = await screen.findByLabelText(/allowed gateway hosts/i)
    // fireEvent rather than userEvent: this is a controlled input whose value
    // never changes, so typing would only ever register the first character.
    const { fireEvent } = await import('@testing-library/react')
    fireEvent.change(field, { target: { value: '*.mail.example.com' } })

    expect(written).toContainEqual(['allowed_hosts', '*.mail.example.com'])
  })
})
