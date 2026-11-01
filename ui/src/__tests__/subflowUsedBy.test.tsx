import { render, screen } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { vi } from 'vitest'
import { server, signInAs } from '../test/setupTests'
import { SubflowInputConfig } from '@/components/workflow/Transformation/configs/logic/SubflowInputConfig'

vi.mock('@tanstack/react-router', () => ({
  useParams: () => ({ id: 'address' }),
}))

describe('Sub-flow input, used by', () => {
  it('lists the steps that run this sub-flow and what each runs', async () => {
    signInAs('Editor')
    server.use(http.get('/api/workflows/address/callers', () => HttpResponse.json({
      data: [
        { workflow_id: 'orders', workflow_name: 'Orders to warehouse', node_id: 'c1', step: 'Clean address', version: 3, latest: false },
        { workflow_id: 'refunds', workflow_name: 'Refunds', node_id: 'c2', step: 'c2', version: 0, latest: true },
      ],
    })))

    render(
      <MantineProvider>
        <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
          <SubflowInputConfig config={{ fields: [] }} updateNodeConfig={() => {}} nodeId="in" />
        </QueryClientProvider>
      </MantineProvider>,
    )

    expect(await screen.findByRole('link', { name: 'Orders to warehouse' })).toHaveAttribute('href', '/workflows/orders')
    expect(screen.getByText('v3')).toBeInTheDocument()
    expect(screen.getByText('Latest saved (not pinned)')).toBeInTheDocument()
  })

  it('says when nothing runs it yet', async () => {
    signInAs('Editor')
    server.use(http.get('/api/workflows/address/callers', () => HttpResponse.json({ data: [] })))
    render(
      <MantineProvider>
        <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
          <SubflowInputConfig config={{ fields: [] }} updateNodeConfig={() => {}} nodeId="in" />
        </QueryClientProvider>
      </MantineProvider>,
    )
    expect(await screen.findByText(/No workflow runs this sub-flow yet/)).toBeInTheDocument()
  })
})
