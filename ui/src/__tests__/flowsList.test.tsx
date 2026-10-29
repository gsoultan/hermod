import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { Notifications } from '@mantine/notifications'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { VHostProvider } from '@/context/VHostContext'
import { ConfirmProvider } from '@/components/common/ConfirmProvider'
import { server, signInAs } from '../test/setupTests'
import WorkflowsPage from '@/pages/workflows/WorkflowsPage'

vi.mock('@tanstack/react-router', () => ({
  Link: ({ children, to, params, search: _s, ...props }: any) => {
    const href = params ? Object.entries(params).reduce((p, [k, v]) => p.replace(`$${k}`, String(v)), to) : to
    return <a href={href} {...props}>{children}</a>
  },
  useNavigate: () => () => {},
}))

const flows = [
  { id: 'wf1', name: 'Orders to warehouse', active: true, status: 'running', vhost: 'prod', workspace_id: 'ws1',
    nodes: [{ id: 'a', type: 'source', data: { label: 'orders-db' } }, { id: 'b', type: 'sink', data: { label: 'snowflake' } }], edges: [] },
  { id: 'wf2', name: 'Payments to ledger', active: true, status: 'error', vhost: 'prod', nodes: [], edges: [] },
  { id: 'wf3', name: 'Nightly export', active: false, status: 'stopped', vhost: 'prod', worker_id: 'w1', nodes: [], edges: [] },
]

let calls: string[] = []

function renderPage() {
  return render(
    <MantineProvider>
      <Notifications />
      <ConfirmProvider>
        <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
          <VHostProvider>
            <WorkflowsPage />
          </VHostProvider>
        </QueryClientProvider>
      </ConfirmProvider>
    </MantineProvider>,
  )
}

beforeEach(() => {
  calls = []
  signInAs('Administrator', 'gembit')
  server.use(
    http.get('/api/vhosts', () => HttpResponse.json({ data: [] })),
    http.get('/api/workspaces', () => HttpResponse.json([{ id: 'ws1', name: 'Sales ops' }])),
    http.get('/api/workers', () => HttpResponse.json({ data: [{ id: 'w1', name: 'worker-eu' }] })),
    http.get('/api/workflows', () => HttpResponse.json({ data: flows, total: flows.length })),
    // The batch routes come first: a ':id' handler would otherwise answer them with id "batch".
    http.post('/api/workflows/batch/toggle', async ({ request }) => { calls.push(`batch-toggle ${JSON.stringify(await request.json())}`); return HttpResponse.json({}) }),
    http.post('/api/workflows/:id/toggle', ({ params }) => { calls.push(`toggle ${params.id}`); return HttpResponse.json({}) }),
    http.post('/api/workflows/batch/delete', async ({ request }) => { calls.push(`batch-delete ${JSON.stringify(await request.json())}`); return HttpResponse.json({}) }),
    http.post('/api/workflows/batch/workspace', async ({ request }) => { calls.push(`batch-workspace ${JSON.stringify(await request.json())}`); return HttpResponse.json({ wf1: 'ok' }) }),
    http.delete('/api/workflows/:id', ({ params }) => { calls.push(`delete ${params.id}`); return HttpResponse.json({}) }),
    http.post('/api/workflows', async ({ request }) => { const b: any = await request.json(); calls.push(`clone ${b.name}`); return HttpResponse.json({}) }),
  )
})

describe('Flows', () => {
  it('is a list of rows: name, from → to, workspace, state, a switch and an arrow', async () => {
    renderPage()
    const list = await screen.findByRole('list', { name: 'Flows' })
    const row = within(list).getAllByRole('listitem')[0]
    expect(within(row).getByRole('link', { name: /Orders to warehouse/ })).toHaveAttribute('href', '/workflows/wf1')
    expect(within(row).getByText(/orders-db → snowflake/)).toBeInTheDocument()
    expect(within(row).getByText('Sales ops')).toBeInTheDocument()
    expect(within(row).getByText('Running')).toBeInTheDocument()
    expect(within(row).getByRole('switch', { name: 'Turn off Orders to warehouse' })).toBeChecked()
    const third = within(list).getAllByRole('listitem')[2]
    expect(within(third).getByText(/worker-eu/)).toBeInTheDocument()
    expect(screen.getByRole('heading', { level: 1, name: 'Flows' })).toBeInTheDocument()
  })

  it('flips the switch with the toggle endpoint', async () => {
    renderPage()
    await userEvent.click(await screen.findByRole('switch', { name: 'Turn on Nightly export' }))
    await waitFor(() => expect(calls).toEqual(['toggle wf3']))
  })

  it('has one filter: All, Running, Problems, Off', async () => {
    renderPage()
    await screen.findByRole('list', { name: 'Flows' })
    await userEvent.click(screen.getByRole('radio', { name: 'Problems' }))
    const rows = within(screen.getByRole('list', { name: 'Flows' })).getAllByRole('listitem')
    expect(rows).toHaveLength(1)
    expect(within(rows[0]).getByText('Payments to ledger')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('radio', { name: 'Off' }))
    expect(within(screen.getByRole('list', { name: 'Flows' })).getAllByRole('listitem')).toHaveLength(1)
  })

  it('keeps Describe it, Templates and Import under New flow ▾', async () => {
    renderPage()
    await screen.findByRole('list', { name: 'Flows' })
    expect(screen.getByRole('link', { name: 'New flow' })).toHaveAttribute('href', '/workflows/new')
    await userEvent.click(screen.getByRole('button', { name: 'More ways to make a flow' }))
    expect(await screen.findByRole('menuitem', { name: /Describe it/, hidden: true })).toBeInTheDocument()
    expect(screen.getByRole('menuitem', { name: /Templates/, hidden: true })).toBeInTheDocument()
    expect(screen.getByRole('menuitem', { name: /Import JSON/, hidden: true })).toBeInTheDocument()
  })

  it('keeps the per-flow actions behind ⋯: Edit flow, Clone, Export JSON, Delete', async () => {
    renderPage()
    await screen.findByRole('list', { name: 'Flows' })
    await userEvent.click(screen.getByRole('button', { name: 'More for Nightly export' }))
    expect(await screen.findByRole('menuitem', { name: /Edit flow/, hidden: true })).toHaveAttribute('href', '/workflows/wf3/edit')
    expect(screen.getByRole('menuitem', { name: /Export JSON/, hidden: true })).toBeInTheDocument()
    await userEvent.click(screen.getByRole('menuitem', { name: /Clone/, hidden: true }))
    await waitFor(() => expect(calls).toEqual(['clone Nightly export (Copy)']))
  })

  it('selecting rows shows the bulk bar: Move to workspace, Turn on, Turn off, Export JSON, Delete', async () => {
    renderPage()
    await screen.findByRole('list', { name: 'Flows' })
    await userEvent.click(screen.getByRole('checkbox', { name: 'Select Orders to warehouse' }))
    await userEvent.click(screen.getByRole('checkbox', { name: 'Select Nightly export' }))
    const bar = screen.getByRole('region', { name: '2 selected' })
    expect(within(bar).getByRole('button', { name: 'Move to workspace' })).toBeInTheDocument()
    expect(within(bar).getByRole('button', { name: 'Export JSON' })).toBeInTheDocument()
    await userEvent.click(within(bar).getByRole('button', { name: 'Turn off' }))
    await waitFor(() => expect(calls).toEqual(['batch-toggle {"ids":["wf1","wf3"],"active":false}']))
    // A batch action clears the selection, so the bar is gone until rows are picked again.
    expect(screen.queryByRole('region', { name: /selected/ })).not.toBeInTheDocument()
    await userEvent.click(screen.getByRole('checkbox', { name: 'Select Orders to warehouse' }))
    await userEvent.click(screen.getByRole('checkbox', { name: 'Select Nightly export' }))
    await userEvent.click(within(screen.getByRole('region', { name: '2 selected' })).getByRole('button', { name: 'Move to workspace' }))
    await userEvent.click(await screen.findByRole('menuitem', { name: 'No workspace', hidden: true }))
    await waitFor(() => expect(calls[1]).toBe('batch-workspace {"ids":["wf1","wf3"],"workspace_id":""}'))
  })

  it('deleting asks first', async () => {
    renderPage()
    await screen.findByRole('list', { name: 'Flows' })
    await userEvent.click(screen.getByRole('button', { name: 'More for Nightly export' }))
    await userEvent.click(await screen.findByRole('menuitem', { name: /Delete/, hidden: true }))
    await userEvent.click(await screen.findByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(calls).toEqual(['delete wf3']))
  })

  it('gives a Viewer no switches, no checkboxes and no New flow', async () => {
    signInAs('Viewer', 'vee')
    renderPage()
    await screen.findByRole('list', { name: 'Flows' })
    expect(screen.queryByRole('switch')).not.toBeInTheDocument()
    expect(screen.queryByRole('checkbox')).not.toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'New flow' })).not.toBeInTheDocument()
  })

  it('says so when there are no flows', async () => {
    server.use(http.get('/api/workflows', () => HttpResponse.json({ data: [], total: 0 })))
    renderPage()
    expect(await screen.findByText('No flows yet')).toBeInTheDocument()
  })
})
