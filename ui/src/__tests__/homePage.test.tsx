import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { Notifications } from '@mantine/notifications'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { VHostProvider } from '@/context/VHostContext'
import { server, signInAs } from '../test/setupTests'
import type { DashboardStats } from '@/hooks/useDashboardStream'
import { HomePage } from '@/pages/home/HomePage'

vi.mock('@tanstack/react-router', () => ({
  Link: ({ children, to, params, search, ...props }: any) => {
    const href = params ? Object.entries(params).reduce((p, [k, v]) => p.replace(`$${k}`, String(v)), to) : to
    const qs = search ? `?${new URLSearchParams(search)}` : ''
    return <a href={`${href}${qs}`} {...props}>{children}</a>
  },
  useNavigate: () => () => {},
}))

const stats: DashboardStats = {
  active_sources: 2, active_sinks: 2, active_workflows: 2, total_processed: 12000, total_lag: 0,
  total_errors: 1, failed_workflows: 1, uptime: 3600, active_workers: 1, total_workflows: 3,
  total_sources: 2, total_sinks: 2, throughput: 1184, error_rate: 0.01, avg_latency_ms: 12,
  backpressure: 0.1, circuit_breakers_open: 0,
  cpu_cores: 4, cpu_usage: 0.2, memory_total_bytes: 0, memory_used_bytes: 0, storage_total_bytes: 0, storage_used_bytes: 0,
} as DashboardStats

let streamStats: DashboardStats = stats

vi.mock('@/hooks/useDashboardStream', async (importOriginal) => {
  const actual = await importOriginal<Record<string, unknown>>()
  return {
    ...actual,
    useDashboardStream: () => ({ stats: streamStats, series: [1100, 1184], connection: 'live', error: null }),
  }
})

const flows = [
  { id: 'wf1', name: 'Orders to warehouse', active: true, status: 'running', vhost: 'prod' },
  { id: 'wf2', name: 'Payments to ledger', active: true, status: 'error', vhost: 'prod' },
  { id: 'wf3', name: 'Nightly export', active: false, status: 'stopped', vhost: 'prod' },
]

let toggled: string[] = []
let bootstrapped = 0

function renderHome() {
  return render(
    <MantineProvider>
      <Notifications />
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <VHostProvider>
          <HomePage now={new Date('2026-10-29T09:00:00')} />
        </VHostProvider>
      </QueryClientProvider>
    </MantineProvider>,
  )
}

beforeEach(() => {
  toggled = []
  bootstrapped = 0
  streamStats = stats
  signInAs('Administrator', 'gembit')
  server.use(
    http.get('/api/vhosts', () => HttpResponse.json({ data: [] })),
    http.get('/api/workflows', () => HttpResponse.json({ data: flows, total: flows.length })),
    http.post('/api/workflows/:id/toggle', ({ params }) => { toggled.push(String(params.id)); return HttpResponse.json({}) }),
    http.get('/api/approvals', () => HttpResponse.json({ data: [{ id: 'a1' }], total: 1 })),
    http.get('/api/dead-letters/stats', () => HttpResponse.json({ queued: 42, last_24h: 42 })),
    http.get('/api/logs', () => HttpResponse.json({ data: [] })),
    http.post('/api/infra/bootstrap-scenario', () => { bootstrapped += 1; return HttpResponse.json({ message: 'ok' }) }),
  )
})

describe('Home', () => {
  it('greets by name and says how the platform is doing in one line', async () => {
    renderHome()
    expect(await screen.findByRole('heading', { level: 1, name: 'Good morning, gembit' })).toBeInTheDocument()
    expect(screen.getByText('2 of 3 flows running · 1,184 messages a second')).toBeInTheDocument()
    expect(screen.getByRole('status')).toHaveTextContent('Live')
  })

  it('lists what needs you, each with the one button that deals with it', async () => {
    renderHome()
    const list = await screen.findByRole('list', { name: 'Needs you' })
    expect(within(list).getByText('Payments to ledger stopped')).toBeInTheDocument()
    expect(within(list).getByRole('link', { name: 'Fix it' })).toHaveAttribute('href', '/workflows/wf2')
    expect(within(list).getByText('42 messages are waiting in Dead letters')).toBeInTheDocument()
    expect(within(list).getByRole('link', { name: 'Retry them' })).toHaveAttribute('href', '/activity?tab=dead-letters')
    expect(within(list).getByText('1 change waiting for your approval')).toBeInTheDocument()
    expect(within(list).getByRole('link', { name: 'Review' })).toHaveAttribute('href', '/activity?tab=approvals')
  })

  it('says so when nothing needs you', async () => {
    streamStats = { ...stats, failed_workflows: 0 }
    server.use(
      http.get('/api/workflows', () => HttpResponse.json({ data: [flows[0]], total: 1 })),
      http.get('/api/approvals', () => HttpResponse.json({ data: [], total: 0 })),
      http.get('/api/dead-letters/stats', () => HttpResponse.json({ queued: 0 })),
    )
    renderHome()
    expect(await screen.findByText('Everything is running normally')).toBeInTheDocument()
    expect(screen.queryByRole('list', { name: 'Needs you' })).not.toBeInTheDocument()
  })

  it('shows your flows with a switch each and a way to the full list', async () => {
    renderHome()
    const list = await screen.findByRole('list', { name: 'Your flows' })
    expect(within(list).getByRole('link', { name: /Orders to warehouse/ })).toHaveAttribute('href', '/workflows/wf1')
    await userEvent.click(within(list).getByRole('switch', { name: 'Turn on Nightly export' }))
    await waitFor(() => expect(toggled).toEqual(['wf3']))
    expect(screen.getByRole('link', { name: 'See all 3 flows' })).toHaveAttribute('href', '/workflows')
    expect(screen.getByRole('link', { name: 'New flow' })).toHaveAttribute('href', '/workflows/new')
  })

  it('gives a Viewer no switches and no New flow', async () => {
    signInAs('Viewer', 'vee')
    renderHome()
    await screen.findByRole('list', { name: 'Your flows' })
    expect(screen.queryByRole('switch')).not.toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'New flow' })).not.toBeInTheDocument()
  })

  it('keeps the platform metrics under the flows', async () => {
    renderHome()
    expect(await screen.findByText('Platform')).toBeInTheDocument()
    expect(screen.getByText('Throughput')).toBeInTheDocument()
    expect(screen.getByText('Circuit Breakers')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Lineage/ })).toBeInTheDocument()
  })

  it('turns into the three-step start when nothing exists yet, with a sample to load', async () => {
    streamStats = { ...stats, total_sources: 0, total_sinks: 0, total_workflows: 0, active_workflows: 0, failed_workflows: 0 }
    server.use(
      http.get('/api/workflows', () => HttpResponse.json({ data: [], total: 0 })),
      http.get('/api/approvals', () => HttpResponse.json({ data: [], total: 0 })),
      http.get('/api/dead-letters/stats', () => HttpResponse.json({ queued: 0 })),
    )
    renderHome()
    const steps = await screen.findByRole('list', { name: 'Get started' })
    expect(within(steps).getAllByRole('listitem').map((li) => li.textContent)).toEqual([
      expect.stringContaining('Connect a source'),
      expect.stringContaining('Connect a sink'),
      expect.stringContaining('Make your first flow'),
    ])
    await userEvent.click(screen.getByRole('button', { name: 'Load a sample' }))
    await waitFor(() => expect(bootstrapped).toBe(1))
    expect(screen.queryByRole('list', { name: 'Your flows' })).not.toBeInTheDocument()
  })
})
