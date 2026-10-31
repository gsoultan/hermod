import { render, screen, waitFor, act } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { vi, afterEach, beforeEach } from 'vitest'
import { server, signInAs } from '../test/setupTests'
import { ConfirmProvider } from '@/components/common/ConfirmProvider'
import { VHostProvider } from '@/context/VHostContext'
import { WorkflowDetailPage } from '@/pages/workflows/WorkflowDetailPage'
import { Layout } from '@/components/layout/Layout'

vi.mock('@tanstack/react-router', () => ({
  Link: ({ children, to: _to, params: _params, ...rest }: any) => <a {...rest}>{children}</a>,
  useParams: () => ({ id: 'wf-1' }),
  useNavigate: () => () => {},
  useSearch: () => ({}),
  useRouterState: () => ({ location: { pathname: '/' }, isLoading: false }),
}))
vi.mock('@/pages/workflows/WorkflowEditor/components/DetailFlowCanvas', () => ({ DetailFlowCanvas: () => null, toViewEdges: () => [] }))
vi.mock('@/pages/workflows/WorkflowDebugger', () => ({ WorkflowDebugger: () => null }))

/** A socket the test can drop, as a restarting server would. */
class FakeSocket {
  static instances: FakeSocket[] = []
  readyState = 0
  onmessage: ((e: any) => void) | null = null
  onopen: (() => void) | null = null
  onclose: (() => void) | null = null
  onerror: (() => void) | null = null
  constructor(public url: string) {
    FakeSocket.instances.push(this)
  }
  send() {}
  close() {
    this.readyState = 3
  }
  /** The server went away: the browser fires close without being asked. */
  drop() {
    this.readyState = 3
    this.onclose?.()
  }
  emit(payload: unknown) {
    this.onmessage?.({ data: JSON.stringify(payload) })
  }
}

const socketsTo = (path: string) => FakeSocket.instances.filter((s) => s.url.includes(path))

beforeEach(() => {
  FakeSocket.instances = []
  vi.stubGlobal('WebSocket', FakeSocket as any)
})
afterEach(() => {
  vi.unstubAllGlobals()
})

/**
 * The detail page's status and log sockets, and the header's dashboard
 * socket, were opened once with no onclose. A server restart or a network
 * blip left them dead for the life of the page: the status badge froze on its
 * last value and the header counts stopped moving, with nothing to say so.
 */
describe('live sockets come back after a drop', () => {
  it('the detail page re-opens its status socket and keeps showing live status', async () => {
    signInAs('Editor')
    server.use(
      http.get('/api/workflows/wf-1', () =>
        HttpResponse.json({ id: 'wf-1', name: 'Orders', vhost: 'default', active: true, status: 'running', nodes: [], edges: [] }),
      ),
      http.get('/api/workflows/wf-1/proposals', () => HttpResponse.json({ data: [] })),
      http.get('/api/logs', () => HttpResponse.json({ data: [], total: 0 })),
    )
    const user = userEvent.setup()
    render(
      <MantineProvider>
        <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
          <ConfirmProvider>
            <WorkflowDetailPage />
          </ConfirmProvider>
        </QueryClientProvider>
      </MantineProvider>,
    )
    await screen.findByText('Orders', {}, { timeout: 10000 })
    await user.click(screen.getByRole('tab', { name: /logs/i }))
    await waitFor(() => expect(socketsTo('/api/ws/status')).toHaveLength(1))
    await waitFor(() => expect(socketsTo('/api/ws/logs')).toHaveLength(1))

    act(() => {
      socketsTo('/api/ws/status')[0].drop()
      socketsTo('/api/ws/logs')[0].drop()
    })

    await waitFor(() => expect(socketsTo('/api/ws/status')).toHaveLength(2), { timeout: 10000 })
    await waitFor(() => expect(socketsTo('/api/ws/logs')).toHaveLength(2), { timeout: 10000 })
    act(() => socketsTo('/api/ws/status')[1].emit({ workflow_id: 'wf-1', engine_status: 'reconnecting' }))
    expect(await screen.findByText(/reconnecting/i, {}, { timeout: 10000 })).toBeInTheDocument()
  }, 30000)

  it('the header re-opens its dashboard socket', async () => {
    signInAs()
    server.use(
      http.get('/api/vhosts', () => HttpResponse.json({ data: [] })),
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
                <div />
              </Layout>
            </VHostProvider>
          </ConfirmProvider>
        </QueryClientProvider>
      </MantineProvider>,
    )
    await waitFor(() => expect(socketsTo('/api/ws/dashboard')).toHaveLength(1))

    act(() => socketsTo('/api/ws/dashboard')[0].drop())

    await waitFor(() => expect(socketsTo('/api/ws/dashboard')).toHaveLength(2), { timeout: 10000 })
  }, 30000)
})
