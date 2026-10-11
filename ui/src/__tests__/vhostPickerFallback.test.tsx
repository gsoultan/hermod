import { render, screen } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { vi } from 'vitest'
import { VHostProvider, useVHost } from '@/context/VHostContext'
import { ConfirmProvider } from '@/components/common/ConfirmProvider'
import { server, signInAs } from '../test/setupTests'
import { Layout } from '@/components/layout/Layout'

vi.mock('@tanstack/react-router', () => ({
  Link: ({ to: _to, params: _params, ...props }: any) => <a {...props} />,
  useNavigate: () => () => {},
  useRouterState: () => ({ location: { pathname: '/' }, isLoading: false }),
}))

/** Shows what the header's vhost picker has to offer. */
function VHostList() {
  const { availableVHosts } = useVHost()
  return <div data-testid="vhosts">{availableVHosts.join(',')}</div>
}

/**
 * A user who may not list vhosts gets them derived from the sources and sinks
 * they can see. That fallback sat behind `if (res.ok)`, but apiFetch throws on
 * any non-OK response, so it never ran: the query failed and the picker was
 * left with nothing but "All".
 */
describe('header vhost picker', () => {
  it('derives the vhosts from sources and sinks when listing them is refused', async () => {
    signInAs('Viewer')
    server.use(
      http.get('/api/vhosts', () => HttpResponse.json({ error: 'forbidden' }, { status: 403 })),
      http.get('/api/sources', () => HttpResponse.json({ data: [{ id: 's1', vhost: 'east' }], total: 1 })),
      http.get('/api/sinks', () => HttpResponse.json({ data: [{ id: 'k1', vhost: 'west' }, { id: 'k2', vhost: 'east' }], total: 2 })),
      http.get('/api/dashboard/stats', () => HttpResponse.json({})),
      http.get('/api/workflows', () => HttpResponse.json({ data: [], total: 0 })),
      http.get('/api/version', () => HttpResponse.json({ version: 'v1.0.0' })),
    )

    render(
      <MantineProvider>
        <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
          <ConfirmProvider>
            <VHostProvider>
              <Layout>
                <VHostList />
              </Layout>
            </VHostProvider>
          </ConfirmProvider>
        </QueryClientProvider>
      </MantineProvider>,
    )

    expect(await screen.findByText('east,west', {}, { timeout: 4000 })).toBeInTheDocument()
  }, 20000)
})
