import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { server, signInAs } from '../test/setupTests'
import { ConnectionsList } from '@/components/connections/ConnectionsList'

vi.mock('@tanstack/react-router', () => ({
  Link: ({ children, to, params, search: _s, ...rest }: any) => {
    const href = params ? Object.entries(params).reduce((p, [k, v]) => p.replace(`$${k}`, String(v)), to) : to
    return <a href={href} {...rest}>{children}</a>
  },
  useNavigate: () => () => {},
}))
vi.mock('@/hooks/useLiveStatuses', () => ({
  useLiveStatuses: () => ({ statuses: { wf1: { source_id: 's2', source_status: 'error' } }, connected: true }),
}))
vi.mock('@/context/VHostContext', () => ({ useVHost: () => ({ selectedVHost: 'all' }) }))

const sources = [
  { id: 's1', name: 'orders-db', type: 'postgresql', vhost: 'prod', active: true, status: 'running', worker_id: 'w1', workspace_id: 'ws1' },
  { id: 's2', name: 'clicks', type: 'kafka', vhost: 'prod', active: true },
]
let deleted: string[] = []

function renderList(kind: 'source' | 'sink' = 'source') {
  return render(
    <MantineProvider>
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })}>
        <ConnectionsList kind={kind} />
      </QueryClientProvider>
    </MantineProvider>,
  )
}

beforeEach(() => {
  deleted = []
  signInAs('Editor')
  server.use(
    http.get('/api/sources', () => HttpResponse.json({ data: sources, total: 2 })),
    http.get('/api/sinks', () => HttpResponse.json({ data: [], total: 0 })),
    http.get('/api/workers', () => HttpResponse.json({ data: [{ id: 'w1', name: 'worker-eu' }], total: 1 })),
    http.get('/api/workspaces', () => HttpResponse.json([{ id: 'ws1', name: 'Sales ops' }])),
    http.get('/api/workflows', () => HttpResponse.json({ data: [{ id: 'wf1', name: 'Orders to warehouse', active: true, nodes: [{ id: 'n1', type: 'source', ref_id: 's1' }] }], total: 1 })),
    http.delete('/api/sources/:id', ({ params }) => { deleted.push(String(params.id)); return HttpResponse.json({}) }),
  )
})

describe('the connections list', () => {
  it('is one row per connection: name, what and where, state, ⋯, into its settings', async () => {
    renderList()
    await screen.findByText('orders-db')
    const list = screen.getByRole('list', { name: 'Sources' })
    const rows = within(list).getAllByRole('listitem')
    expect(rows).toHaveLength(2)
    expect(within(rows[0]).getByText('orders-db')).toBeInTheDocument()
    expect(within(rows[0]).getByText('postgresql · prod · worker-eu · Sales ops')).toBeInTheDocument()
    expect(within(rows[0]).getByText('Online')).toBeInTheDocument()
    expect(within(rows[1]).getByText('Offline')).toBeInTheDocument()
    expect(within(rows[0]).getByText('Used by 1 flow')).toBeInTheDocument()
    expect(within(rows[1]).getByText('Not used')).toBeInTheDocument()
    expect(within(rows[0]).getByRole('link', { name: /orders-db/ })).toHaveAttribute('href', '/sources/s1/edit')
    expect(screen.getByRole('searchbox', { name: 'Search sources' })).toBeInTheDocument()
    expect(screen.getAllByLabelText('Filter by workspace').length).toBeGreaterThan(0)
  })

  it('asks before deleting and names the running flows that use it', async () => {
    renderList()
    await screen.findByText('orders-db')
    await userEvent.click(screen.getByRole('button', { name: 'More for orders-db' }))
    await userEvent.click(await screen.findByRole('menuitem', { name: 'Delete', hidden: true }))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText('Orders to warehouse')).toBeInTheDocument()
    await userEvent.click(within(dialog).getByRole('button', { name: 'Delete source' }))
    expect(deleted).toEqual(['s1'])
  })

  it('gives a Viewer no ⋯', async () => {
    signInAs('Viewer')
    renderList()
    await screen.findByText('orders-db')
    expect(screen.queryByRole('button', { name: /More for/ })).not.toBeInTheDocument()
  })

  it('says so when there is nothing, with one button', async () => {
    renderList('sink')
    expect(await screen.findByText('No sinks yet')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Add sink' })).toHaveAttribute('href', '/sinks/new')
  })
})
