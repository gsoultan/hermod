import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { describe, it, expect, vi } from 'vitest'
import { SinkWizard } from '@/components/forms/SinkWizard'
import { SINK_TYPES, configComponents } from '@/components/forms/SinkForm'

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => () => {},
  Link: (props: any) => <button {...props} />,
}))

const noop = () => {}
const mutation = (mutate: any) => ({ mutate, isPending: false, isError: false, error: null })
const ui = (node: React.ReactNode) => render(<MantineProvider>{node}</MantineProvider>)

function wizardProps(type: string, config: Record<string, string>, updateConfig = noop) {
  return {
    sink: { name: 'dest', type, vhost: '/', worker_id: '', config },
    isEditing: false,
    embedded: false,
    availableVHostsList: ['/'],
    workers: [],
    sinkTypes: SINK_TYPES,
    testMutation: mutation(noop),
    submitMutation: mutation(noop),
    testResult: { status: 'ok', message: 'Connection successful' },
    setTestResult: noop,
    updateConfig,
    handleSinkChange: noop,
    onCancel: noop,
    configComponents,
    availableFields: [],
  } as any
}

async function gotoConnectionStep(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole('button', { name: /next step/i }))
  expect(screen.getByText(/Step 2: Connection Settings/i)).toBeInTheDocument()
}

/**
 * "API / Webhook" is sink type `http`. It had no entry in configComponents, so
 * SinkWizard fell through to the *database* form — which asks for a host and a
 * table, and writes no `url`. Since SINK_REQUIREMENTS.http demands a `url`, the
 * Next button it gates could never enable: the sink was unconfigurable from
 * every entry point at once.
 *
 * This walks the chain the user walks, rather than asserting on the map.
 */
describe('the API / Webhook connection step', () => {
  it('renders its own fields, not the database form', async () => {
    const user = userEvent.setup()
    ui(<SinkWizard {...wizardProps('http', {})} />)
    await gotoConnectionStep(user)

    await waitFor(() =>
      expect(screen.getByPlaceholderText('https://api.example.com/ingest')).toBeInTheDocument(),
    )
    expect(screen.getByPlaceholderText(/Authorization: Bearer token/)).toBeInTheDocument()
    // Compression and timeout are read by the factory and had no input anywhere.
    expect(screen.getByPlaceholderText('30s')).toBeInTheDocument()
    expect(screen.getByText(/^Compression$/)).toBeInTheDocument()

    // The database form's fields must not be here.
    expect(screen.queryByPlaceholderText('5432')).not.toBeInTheDocument()
  })

  it('writes the url key the requirements gate reads', async () => {
    const user = userEvent.setup()
    const updateConfig = vi.fn()
    ui(<SinkWizard {...wizardProps('http', {}, updateConfig)} />)
    await gotoConnectionStep(user)

    const url = await screen.findByPlaceholderText('https://api.example.com/ingest')
    await user.type(url, 'h')

    expect(updateConfig).toHaveBeenCalledWith('url', 'h')
  })

  it('keeps Next disabled until the url is set, then enables it', async () => {
    const user = userEvent.setup()
    const { unmount } = ui(<SinkWizard {...wizardProps('http', {})} />)
    await gotoConnectionStep(user)
    expect(screen.getByRole('button', { name: /next step/i })).toBeDisabled()
    unmount()

    ui(<SinkWizard {...wizardProps('http', { url: 'https://api.example.com/ingest' })} />)
    await gotoConnectionStep(user)
    expect(screen.getByRole('button', { name: /next step/i })).toBeEnabled()
  })
})

describe('the Panmail connection step', () => {
  it('collects the five settings the gateway refuses to guess', async () => {
    const user = userEvent.setup()
    ui(<SinkWizard {...wizardProps('panmail', {})} />)
    await gotoConnectionStep(user)

    for (const placeholder of [
      'https://mail.example.com',
      'Paste the key',
      '0f8b…',
      'noreply@example.com',
      '{{.email}}',
    ]) {
      await waitFor(() => expect(screen.getByPlaceholderText(placeholder)).toBeInTheDocument())
    }
    expect(screen.getByRole('button', { name: /next step/i })).toBeDisabled()
  })

  it('enables Next once all five are filled', async () => {
    const user = userEvent.setup()
    ui(
      <SinkWizard
        {...wizardProps('panmail', {
          base_url: 'https://mail.example.com',
          api_key: 'pk_test',
          provider_id: '0f8b',
          from: 'noreply@example.com',
          to: '{{.email}}',
        })}
      />,
    )
    await gotoConnectionStep(user)
    await waitFor(() =>
      expect(screen.getByRole('button', { name: /next step/i })).toBeEnabled(),
    )
  })
})
