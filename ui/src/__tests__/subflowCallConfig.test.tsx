import { render, screen } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { vi } from 'vitest'
import { server, signInAs } from '../test/setupTests'
import { SubflowCallConfig } from '@/components/workflow/Transformation/configs/logic/SubflowCallConfig'

vi.mock('@tanstack/react-router', () => ({
  useParams: () => ({ id: 'caller' }),
}))

const subflowNodes = [
  { id: 'in', type: 'subflow_input', config: { fields: [{ name: 'zip', required: true }, { name: 'street' }] } },
  { id: 'r', type: 'subflow_return', config: { name: 'valid' } },
]

function renderConfig(config: any) {
  return render(
    <MantineProvider>
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <SubflowCallConfig config={config} updateNodeConfig={() => {}} nodeId="call" availableFields={['customer.zip']} />
      </QueryClientProvider>
    </MantineProvider>,
  )
}

describe('Run sub-flow editor', () => {
  // The version list comes back without each version's steps (nodes: null);
  // reading the inputs from it crashed the editor the first time it opened.
  it('shows the pinned version’s inputs, read from that version', async () => {
    signInAs('Editor')
    server.use(
      http.get('/api/workflows', () => HttpResponse.json({
        data: [
          { id: 'sf', name: 'Clean address', nodes: subflowNodes },
          { id: 'plain', name: 'Not a sub-flow', nodes: [{ id: 's', type: 'source' }] },
        ],
        total: 2,
      })),
      http.get('/api/workflows/sf/versions', () => HttpResponse.json([{ version: 3, message: 'tidy', nodes: null }])),
      http.get('/api/workflows/sf/versions/3', () => HttpResponse.json({ version: 3, nodes: subflowNodes })),
    )

    renderConfig({ subflow_id: 'sf', version: 3, inputs: [{ from: 'customer.zip', to: 'zip' }] })

    expect(await screen.findByText('zip *')).toBeInTheDocument()
    expect(screen.getByText('street')).toBeInTheDocument()
    expect(screen.getByDisplayValue('customer.zip')).toBeInTheDocument()
  })
})

describe('Run sub-flow editor, what it runs', () => {
  it('lists the steps of the pinned version and says when a newer one is saved', async () => {
    signInAs('Editor')
    const v3 = [...subflowNodes, { id: 'geo', type: 'transformation', config: { label: 'Geo lookup' } }]
    server.use(
      http.get('/api/workflows', () => HttpResponse.json({ data: [{ id: 'sf', name: 'Clean address', nodes: v3 }], total: 1 })),
      http.get('/api/workflows/sf/versions', () => HttpResponse.json([
        { version: 3, nodes: null }, { version: 4, nodes: null },
      ])),
      http.get('/api/workflows/sf/versions/3', () => HttpResponse.json({ version: 3, nodes: v3 })),
    )

    renderConfig({ subflow_id: 'sf', version: 3, inputs: [] })

    expect(await screen.findByText('Geo lookup')).toBeInTheDocument()
    expect(await screen.findByText(/v4 is saved/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Use v4' })).toBeInTheDocument()
  })
})
