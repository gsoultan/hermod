import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { server } from '../test/setupTests'
import { SourceDiagnosis } from '@/components/connections/SourceDiagnosis'
import type { DiagnosisReport } from '@/lib/dbDiagnosis'

const report: DiagnosisReport = {
  route: { through_pooler: true, server_port: 5432, dialed_port: 6432, server_version: '16.4' },
  checks: [
    { id: 'locks', title: 'Locks', status: 'warn', value: '1 waiting', detail: 'dba · psql, CREATE INDEX CONCURRENTLY has waited 22 min on Hermod (hermod), WITH (running 1 h 57 min).' },
    { id: 'long_queries', title: 'Long queries', status: 'warn', value: '2', detail: "2 running for more than 5 minutes, 2 of them Hermod's." },
    { id: 'pooler', title: 'PgBouncer pool', status: 'unknown', value: 'Not readable', detail: 'PgBouncer refused to show its pool figures to this user (SQLSTATE 08P01).' },
  ],
  hermod: [
    { pid: 10, user: 'hermod', app: 'hermod', state: 'active', wait_type: 'IO', command: 'WITH', query_seconds: 7020, xact_seconds: 7020, state_seconds: 7020, hermod: true },
    { pid: 12, user: 'hermod', app: 'hermod-sql-steps', state: 'idle', query_seconds: 0, xact_seconds: 0, state_seconds: 3, hermod: true },
  ],
  others: [{ pid: 20, user: 'dba', app: 'psql', state: 'active', wait_type: 'Lock', command: 'CREATE INDEX CONCURRENTLY', query_seconds: 1320, xact_seconds: 1320, state_seconds: 1320, blocked_by: [10], hermod: false }],
  checked_at: '2026-11-28T05:14:00Z',
}

function renderIt(canCheck = true) {
  return render(
    <MantineProvider>
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <SourceDiagnosis sourceId="src-1" canCheck={canCheck} />
      </QueryClientProvider>
    </MantineProvider>,
  )
}

describe('SourceDiagnosis', () => {
  it('shows the route, what Hermod is running, and every check', async () => {
    let calls = 0
    server.use(http.post('/api/sources/src-1/diagnosis', () => { calls++; return HttpResponse.json(report) }))
    renderIt()
    expect(await screen.findByText('PgBouncer')).toBeInTheDocument()
    expect(screen.getByText('PostgreSQL 16.4')).toBeInTheDocument()
    expect(screen.getByText(/listens on 5432, Hermod dials 6432/)).toBeInTheDocument()

    const row = screen.getByText('1 h 57 min').closest('tr') as HTMLElement
    expect(within(row).getByText('hermod')).toBeInTheDocument()
    // The idle connection is not listed as running.
    expect(document.querySelector('[data-session="12"]')).toBeNull()

    const locks = document.querySelector('[data-check="locks"]') as HTMLElement
    expect(locks.dataset.status).toBe('warn')
    expect(within(locks).getByText(/CREATE INDEX CONCURRENTLY has waited 22 min/)).toBeInTheDocument()
    expect(document.querySelector('[data-check="pooler"]')?.getAttribute('data-status')).toBe('unknown')
    expect(within(document.querySelector('[data-other="20"]') as HTMLElement).getByText(/blocked by 1 session/)).toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: 'Check again' }))
    await vi.waitFor(() => expect(calls).toBe(2))
  })

  it('says when the database could not be reached, and lets you try again', async () => {
    server.use(http.post('/api/sources/src-1/diagnosis', () => HttpResponse.json({ error: 'could not reach the database: connection refused' }, { status: 502 })))
    renderIt()
    expect(await screen.findByText('Hermod could not diagnose this database')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Try again' })).toBeInTheDocument()
  })

  it('does not connect for a viewer', () => {
    let calls = 0
    server.use(http.post('/api/sources/src-1/diagnosis', () => { calls++; return HttpResponse.json(report) }))
    renderIt(false)
    expect(screen.getByText(/takes an Editor/)).toBeInTheDocument()
    expect(calls).toBe(0)
  })
})
