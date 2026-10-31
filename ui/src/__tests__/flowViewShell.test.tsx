import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ConfirmProvider } from '@/components/common/ConfirmProvider'
import { server, signInAs } from '../test/setupTests'
import { WorkflowDetailPage } from '@/pages/workflows/WorkflowDetailPage'

let search: Record<string, string> = {}

vi.mock('@tanstack/react-router', () => ({
  Link: ({ children, to, params, search: _s, ...rest }: any) => {
    const href = params ? Object.entries(params).reduce((p, [k, v]) => p.replace(`$${k}`, String(v)), to) : to
    return <a href={href} {...rest}>{children}</a>
  },
  useParams: () => ({ id: 'wf-1' }),
  useNavigate: () => () => {},
  useSearch: () => search,
}))
vi.mock('@/pages/workflows/WorkflowEditor/components/DetailFlowCanvas', () => ({ DetailFlowCanvas: () => <div>canvas</div>, toViewEdges: () => [] }))
vi.mock('@/pages/workflows/WorkflowDebugger', () => ({ WorkflowDebugger: () => null }))

function renderPage() {
  return render(
    <MantineProvider>
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })}>
        <ConfirmProvider>
          <WorkflowDetailPage />
        </ConfirmProvider>
      </QueryClientProvider>
    </MantineProvider>,
  )
}

beforeEach(() => {
  search = {}
  signInAs('Editor')
  server.use(
    http.get('/api/workflows/wf-1', () =>
      HttpResponse.json({ id: 'wf-1', name: 'Orders', vhost: 'prod', active: true, status: 'running', nodes: [{ id: 'src', type: 'source', config: {} }], edges: [] }),
    ),
    http.get('/api/workflows/wf-1/proposals', () => HttpResponse.json({ data: [{ id: 'p1', status: 'pending', title: 'Retry longer' }] })),
    http.get('/api/workflows/wf-1/executions', () => HttpResponse.json({ executions: [] })),
    http.get('/api/environments/self', () => HttpResponse.json({ role: 'development', name: 'dev' })),
    http.get('/api/workflows/wf-1/versions', () => HttpResponse.json([])),
  )
})

describe('a flow, opened read only', () => {
  it('has the seven tabs, with the proposals count on Tuning', async () => {
    renderPage()
    await screen.findByText('Orders')
    const tabs = screen.getAllByRole('tab').map((t) => t.textContent?.replace(/\d+$/, '').trim())
    expect(tabs).toEqual(['Build', 'Runs', 'Traces', 'Logs', 'History', 'Debugger', 'Tuning'])
    expect(within(screen.getByRole('tab', { name: /Tuning/ })).getByText('1')).toBeInTheDocument()
    expect(screen.getByRole('tab', { name: /Build/ })).toHaveAttribute('aria-selected', 'true')
  })

  it('opens on the tab in the URL, old names included', async () => {
    search = { tab: 'executions' }
    renderPage()
    await screen.findByText('Orders')
    expect(screen.getByRole('tab', { name: /Runs/ })).toHaveAttribute('aria-selected', 'true')
  })

  it('keeps the header to Run with input, Stop, Edit flow and ⋯', async () => {
    renderPage()
    await screen.findByText('Orders')
    expect(screen.getByRole('link', { name: 'Back to flows' })).toHaveAttribute('href', '/workflows')
    expect(screen.getByRole('button', { name: 'Run with input' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Stop' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Edit flow' })).toHaveAttribute('href', '/workflows/wf-1/edit')
    expect(screen.getAllByRole('link', { name: 'Edit flow' })).toHaveLength(1)
    await userEvent.click(screen.getByRole('button', { name: 'More' }))
    for (const item of ['Export JSON', 'Clone', 'Promote', 'Version history', 'Compliance report', 'Delete flow']) {
      expect(await screen.findByRole('menuitem', { name: item, hidden: true })).toBeInTheDocument()
    }
  })

  it('gives a Viewer no Run with input, no Edit flow and no ⋯', async () => {
    signInAs('Viewer')
    renderPage()
    await screen.findByText('Orders')
    expect(screen.queryByRole('button', { name: 'Run with input' })).not.toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'Edit flow' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'More' })).not.toBeInTheDocument()
  })
})
