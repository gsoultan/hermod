import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { vi } from 'vitest'
import { server } from '../test/setupTests'
import { DescribeAutomationModal } from '@/pages/workflows/builder/DescribeAutomationModal'

const draftResponse = {
  workflow: {
    id: '',
    name: 'Triage support mail',
    vhost: 'support',
    active: false,
    nodes: [
      { id: 'in', type: 'source', ref_id: 'src-1', x: 0, y: 0, config: { label: 'Inbox' } },
      { id: 'map', type: 'transformation', x: 250, y: 0, config: { transType: 'mapping' } },
      { id: 'out', type: 'sink', x: 500, y: 0, config: {} },
    ],
    edges: [
      { id: 'e1', source_id: 'in', target_id: 'map' },
      { id: 'e2', source_id: 'map', target_id: 'out' },
    ],
  },
  issues: [
    { severity: 'error', message: "Node 'out' needs a sink.", recommendation: 'Choose the sink in the node settings.', node_id: 'out' },
    { severity: 'warning', message: 'The mapping has no rules yet.', recommendation: '' },
  ],
  saved: false,
  provider: 'anthropic',
  model: 'claude-opus-5-5',
  usage: { input_tokens: 1200, output_tokens: 300 },
}

function renderModal(onOpenInEditor = vi.fn()) {
  server.use(http.get('/api/vhosts/:vhost/secrets', () => HttpResponse.json({ data: [{ name: 'ANTHROPIC_KEY' }] })))
  render(
    <MantineProvider>
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })}>
        <DescribeAutomationModal
          opened
          onClose={() => {}}
          defaultVHost="support"
          availableVHosts={['support', 'billing']}
          onOpenInEditor={onOpenInEditor}
        />
      </QueryClientProvider>
    </MantineProvider>,
  )
  return onOpenInEditor
}

async function fillConnection(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole('combobox', { name: /^provider/i }))
  await user.click(await screen.findByRole('option', { name: 'Claude (Anthropic)', hidden: true }))
  fireEvent.change(screen.getByRole('textbox', { name: /^model/i }), { target: { value: 'claude-opus-5-5' } })
  fireEvent.change(screen.getByRole('combobox', { name: /api key secret/i }), { target: { value: 'ANTHROPIC_KEY' } })
}

const describeBox = () => screen.getByRole('textbox', { name: /describe the automation/i })

describe('Describe an automation', () => {
  it('needs a description and a connection before it drafts', async () => {
    const user = userEvent.setup()
    renderModal()
    const draft = () => screen.getByRole('button', { name: /^draft workflow/i })
    expect(draft()).toBeDisabled()

    fireEvent.change(describeBox(), { target: { value: 'When a support mail arrives, classify it and file a ticket.' } })
    expect(draft()).toBeDisabled()

    await fillConnection(user)
    await waitFor(() => expect(draft()).toBeEnabled())

    fireEvent.change(describeBox(), { target: { value: 'x'.repeat(4001) } })
    expect(screen.getByText(/4,?001 \/ 4,?000/)).toBeInTheDocument()
    expect(draft()).toBeDisabled()
  })

  it('sends the key as a secret reference and previews the unsaved draft with its issues', async () => {
    let body: any
    server.use(
      http.post('/api/ai/build-workflow', async ({ request }) => {
        body = await request.json()
        return HttpResponse.json(draftResponse)
      }),
    )
    const user = userEvent.setup()
    const onOpenInEditor = renderModal()

    fireEvent.change(describeBox(), { target: { value: 'Classify support mail' } })
    await fillConnection(user)
    await user.click(screen.getByRole('button', { name: /^draft workflow/i }))

    await waitFor(() =>
      expect(body).toEqual({
        description: 'Classify support mail',
        vhost: 'support',
        connection: { provider: 'anthropic', model: 'claude-opus-5-5', apiKey: '{{secret("ANTHROPIC_KEY")}}', baseUrl: '' },
      }),
    )

    expect(await screen.findByText('Triage support mail')).toBeInTheDocument()
    const nodes = screen.getByRole('table', { name: /draft nodes/i })
    expect(within(nodes).getByText('Inbox')).toBeInTheDocument()
    expect(within(nodes).getByText('mapping')).toBeInTheDocument()
    expect(screen.getByText(/in → map/)).toBeInTheDocument()
    expect(screen.getByText("Node 'out' needs a sink.")).toBeInTheDocument()
    expect(screen.getByText('Choose the sink in the node settings.')).toBeInTheDocument()
    expect(screen.getByText('The mapping has no rules yet.')).toBeInTheDocument()
    expect(screen.getByText(/nothing has been saved/i)).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: /open in editor/i }))
    expect(onOpenInEditor).toHaveBeenCalledWith(draftResponse.workflow)
  })

  it('shows why drafting failed', async () => {
    server.use(
      http.post('/api/ai/build-workflow', () => HttpResponse.json({ error: 'Drafting failed: model unavailable' }, { status: 502 })),
    )
    const user = userEvent.setup()
    renderModal()
    fireEvent.change(describeBox(), { target: { value: 'Classify support mail' } })
    await fillConnection(user)
    await user.click(screen.getByRole('button', { name: /^draft workflow/i }))
    expect(await screen.findByText(/model unavailable/)).toBeInTheDocument()
  })
})
