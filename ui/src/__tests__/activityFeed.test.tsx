import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { server, signInAs } from '../test/setupTests'
import { ConfirmProvider } from '@/components/common/ConfirmProvider'
import { ActivityFeed } from '@/pages/activity/ActivityFeed'

let search: Record<string, unknown> = {}
vi.mock('@tanstack/react-router', () => ({
  Link: ({ children, to, params, search: s, ...rest }: any) => {
    let href = params ? Object.entries(params).reduce((p: string, [k, v]) => p.replace(`$${k}`, String(v)), to) : to
    if (s) href += '?' + new URLSearchParams(s).toString()
    return <a href={href} {...rest}>{children}</a>
  },
  useSearch: () => search,
}))

const logs = [
  { id: 'l1', timestamp: '2026-10-29T09:41:00Z', level: 'ERROR', message: 'Sink timed out after 30s', workflow_id: 'wf1', sink_id: 'k1' },
  { id: 'l2', timestamp: '2026-10-29T08:30:00Z', level: 'INFO', action: 'start', username: 'gembit', message: 'Workflow started', workflow_id: 'wf2', data: '{"a":1}' },
  { id: 'l3', timestamp: '2026-10-29T07:58:00Z', level: 'WARN', message: 'Connection lost, reconnecting', source_id: 's1' },
]
let requested: string[] = []
let cleared = 0

function renderFeed() {
  return render(
    <MantineProvider>
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <ConfirmProvider>
          <ActivityFeed />
        </ConfirmProvider>
      </QueryClientProvider>
    </MantineProvider>,
  )
}

beforeEach(() => {
  search = {}
  requested = []
  cleared = 0
  signInAs('Administrator')
  server.use(
    http.get('/api/logs', ({ request }) => {
      requested.push(new URL(request.url).search)
      return HttpResponse.json({ data: logs, total: 3 })
    }),
    http.delete('/api/logs', () => { cleared++; return HttpResponse.json({}) }),
    http.get('/api/workflows', () => HttpResponse.json({ data: [{ id: 'wf1', name: 'Billing sync' }, { id: 'wf2', name: 'New customers to CRM' }], total: 2 })),
    http.get('/api/sources', () => HttpResponse.json({ data: [{ id: 's1', name: 'crm-api' }], total: 1 })),
    http.get('/api/sinks', () => HttpResponse.json({ data: [{ id: 'k1', name: 'kafka' }], total: 1 })),
  )
})

describe('the activity feed', () => {
  it('is one row per event: what it is about by name, what happened, and the one thing to do', async () => {
    renderFeed()
    const list = await screen.findByRole('list', { name: 'Activity' })
    await within(list).findByText('Billing sync')
    const rows = within(list).getAllByRole('listitem')
    expect(rows).toHaveLength(3)
    expect(within(rows[0]).getByText('Sink timed out after 30s')).toBeInTheDocument()
    expect(within(rows[0]).getByRole('link', { name: 'Open flow' })).toHaveAttribute('href', '/workflows/wf1?tab=logs')
    expect(within(rows[1]).getByText('New customers to CRM')).toBeInTheDocument()
    expect(within(rows[1]).getByText('Workflow started · by gembit')).toBeInTheDocument()
    expect(within(rows[1]).queryByRole('link', { name: 'Open flow' })).not.toBeInTheDocument()
    expect(within(rows[2]).getByText('crm-api')).toBeInTheDocument()
  })

  it('asks the endpoint for problems or warnings only', async () => {
    renderFeed()
    await screen.findByText('Billing sync')
    await userEvent.click(screen.getByText('Problems'))
    await vi.waitFor(() => expect(requested.at(-1)).toContain('level=ERROR'))
    await userEvent.click(screen.getByText('Warnings'))
    await vi.waitFor(() => expect(requested.at(-1)).toContain('level=WARN'))
  })

  it('opens the whole entry, payload included, from its row', async () => {
    renderFeed()
    await userEvent.click(await screen.findByText('Workflow started · by gembit'))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText('Workflow started')).toBeInTheDocument()
    expect(within(dialog).getByText(/"a": 1/)).toBeInTheDocument()
    expect(within(dialog).getByText('gembit')).toBeInTheDocument()
  })

  it('keeps the flow, level and action filters, and starts from a flow in the URL', async () => {
    search = { workflow_id: 'wf1' }
    renderFeed()
    await screen.findByText('Billing sync')
    expect(requested[0]).toContain('workflow_id=wf1')
    await userEvent.click(screen.getByRole('button', { name: /Filters/ }))
    expect(await screen.findByRole('textbox', { name: 'Flow ID' })).toHaveValue('wf1')
    expect(screen.getAllByLabelText('Level').length).toBeGreaterThan(0)
    expect(screen.getAllByLabelText('Action').length).toBeGreaterThan(0)
  })

  it('clears every log only after asking', async () => {
    renderFeed()
    await screen.findByText('Billing sync')
    await userEvent.click(screen.getByRole('button', { name: 'More' }))
    await userEvent.click(await screen.findByRole('menuitem', { name: 'Clear all logs', hidden: true }))
    const dialog = await screen.findByRole('dialog')
    expect(cleared).toBe(0)
    await userEvent.click(within(dialog).getByRole('button', { name: 'Clear logs' }))
    await vi.waitFor(() => expect(cleared).toBe(1))
  })
})
