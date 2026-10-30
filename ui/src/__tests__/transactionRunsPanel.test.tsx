import { render, screen, within } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { describe, it, expect } from 'vitest'
import { http, HttpResponse } from 'msw'
import { server } from '../test/setupTests'
import { TransactionRunsPanel } from '@/pages/workflows/executions/TransactionRunsPanel'

/**
 * The runs of a workflow's transactions, on the flow's Runs tab.
 *
 * Each transaction shows its totals, each database's share, what the
 * transaction log holds now, and its latest runs with the database that ended
 * a failed one. A workflow with no transaction shows nothing at all.
 */
const WF = 'wf-1'

function transaction(overrides: Record<string, unknown> = {}) {
  return {
    name: 'order',
    kind: 'commit_together',
    require_every_member: true,
    members: [
      { id: 'pg', name: 'Orders', sink_id: 'pg-1', sink_type: 'postgres' },
      { id: 'my', name: 'Invoices', sink_id: 'my-1', sink_type: 'mysql' },
    ],
    stats: {
      committed: 41,
      rolled_back: 2,
      in_doubt: 0,
      refused: 3,
      members: [
        { id: 'pg', committed: 41, rolled_back: 2, in_doubt: 0, missing: 0 },
        {
          id: 'my', committed: 41, rolled_back: 2, in_doubt: 0, missing: 3,
          last_error: 'member "my": CHECK constraint failed', last_error_at: '2026-10-30T04:00:00Z',
        },
      ],
      recent: [
        {
          at: '2026-10-30T04:01:00Z', outcome: 'refused', duration_ms: 0,
          members: [{ id: 'pg', rows: 1 }, { id: 'my', rows: 0, missing: true }],
          error: 'every database in this transaction must get a row, and this event produced none for Invoices',
        },
        {
          at: '2026-10-30T04:00:00Z', outcome: 'rolled_back', duration_ms: 12,
          members: [{ id: 'pg', rows: 2 }, { id: 'my', rows: 1, failed: true }],
          error: 'member "my": CHECK constraint failed',
        },
        { at: '2026-10-30T03:59:00Z', outcome: 'committed', duration_ms: 8, members: [{ id: 'pg', rows: 1 }, { id: 'my', rows: 1 }] },
      ],
    },
    pending: [],
    ...overrides,
  }
}

function renderPanel() {
  return render(
    <MantineProvider>
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <TransactionRunsPanel workflowId={WF} />
      </QueryClientProvider>
    </MantineProvider>,
  )
}

describe('the transaction runs panel', () => {
  it('shows each transaction with its totals, its databases and its latest runs', async () => {
    server.use(http.get(`/api/workflows/${WF}/transactions`, () => HttpResponse.json({ transactions: [transaction()] })))
    renderPanel()

    const card = await screen.findByTestId('transaction-order')
    expect(within(card).getByText(/“order”/)).toBeInTheDocument()
    expect(within(card).getByText(/every database must get a row/i)).toBeInTheDocument()
    expect(within(card).getByTestId('stat-committed')).toHaveTextContent('41')
    expect(within(card).getByTestId('stat-rolled_back')).toHaveTextContent('2')
    expect(within(card).getByTestId('stat-refused')).toHaveTextContent('3')
    expect(within(card).getByTestId('stat-pending')).toHaveTextContent('0')

    const members = within(card).getByRole('table', { name: /databases/i })
    const invoices = within(members).getByRole('row', { name: /Invoices/ })
    expect(invoices).toHaveTextContent('CHECK constraint failed')
    expect(invoices).toHaveTextContent('3')

    const runs = within(card).getByRole('table', { name: /latest runs/i })
    const rows = within(runs).getAllByRole('row').slice(1)
    expect(rows).toHaveLength(3)
    expect(rows[0]).toHaveTextContent(/refused/i)
    expect(rows[0]).toHaveTextContent(/Invoices: no row/)
    expect(rows[1]).toHaveTextContent(/rolled back/i)
    expect(rows[1]).toHaveTextContent(/Invoices ×1 \(failed\)/)
    expect(rows[2]).toHaveTextContent(/committed/i)
  })

  it('warns about transactions the log still holds, naming the databases', async () => {
    server.use(
      http.get(`/api/workflows/${WF}/transactions`, () =>
        HttpResponse.json({
          transactions: [transaction({
            pending: [{ tx_id: 't1', state: 'committing', started_at: '2026-10-30T04:02:00Z', participants: ['my'] }],
          })],
        }),
      ),
    )
    renderPanel()
    expect(await screen.findByTestId('stat-pending')).toHaveTextContent('1')
    const list = screen.getByTestId('pending-list')
    expect(list).toHaveTextContent(/commit decided/i)
    expect(list).toHaveTextContent(/waiting on Invoices/)
  })

  it('says when a transaction has not run yet, and why its runs cannot be read', async () => {
    server.use(
      http.get(`/api/workflows/${WF}/transactions`, () =>
        HttpResponse.json({
          transactions: [transaction({
            require_every_member: false,
            stats: { committed: 0, rolled_back: 0, in_doubt: 0, refused: 0, members: [], recent: [] },
            problem: 'a state store is required',
          })],
        }),
      ),
    )
    renderPanel()
    expect(await screen.findByText(/no runs yet/i)).toBeInTheDocument()
    expect(screen.getByText(/a state store is required/i)).toBeInTheDocument()
    expect(screen.queryByText(/every database must get a row/i)).not.toBeInTheDocument()
  })

  it('shows nothing for a workflow without transactions', async () => {
    let asked = false
    server.use(http.get(`/api/workflows/${WF}/transactions`, () => {
      asked = true
      return HttpResponse.json({ transactions: [] })
    }))
    const { container } = renderPanel()
    await new Promise((r) => setTimeout(r, 50))
    expect(asked).toBe(true)
    expect(container.querySelector('[data-testid^="transaction-"]')).toBeNull()
  })
})
