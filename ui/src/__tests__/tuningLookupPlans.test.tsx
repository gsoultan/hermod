import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { Notifications } from '@mantine/notifications'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { server } from '../test/setupTests'
import { mutationCache } from '@/lib/queryClient'
import { LookupPlans } from '@/pages/workflows/tuning/LookupPlans'
import type { LookupPlanNode } from '@/lib/insights'

const PROPOSAL = 'CREATE INDEX CONCURRENTLY IF NOT EXISTS sales_orders_trim_lower_doc_no_idx ON public.sales_orders ((TRIM(BOTH FROM lower(doc_no))));'

const nodes: LookupPlanNode[] = [
  {
    node_id: 'n1', label: 'Find order', source: 'ETL', sql: 'SELECT id FROM public.sales_orders WHERE doc_no = $1',
    scans: [], tables: [],
    findings: [{ rule: 'index_used', severity: 'ok', table: 'public.sales_orders', title: 'Uses index so_doc', detail: 'The query reads public.sales_orders through so_doc, a btree index on (doc_no).' }],
  },
  {
    node_id: 'n2', label: 'Delete items', source: 'ETL', sql: 'SELECT s.id FROM etl."SODTL" sa JOIN public.sales_orders s ON TRIM(LOWER(sa."DocNo")) = TRIM(LOWER(s.doc_no))',
    scans: [], tables: [],
    findings: [{
      rule: 'wrapped_column', severity: 'warn', table: 'public.sales_orders',
      title: 'A function on the column stops index use on public.sales_orders',
      detail: 'The index so_doc on doc_no cannot be used for that.', proposal: PROPOSAL,
    }],
  },
  { node_id: 'n3', label: 'Counter', source: 'Shop', sql: '', scans: [], tables: [], findings: [], error: 'only PostgreSQL sources can be checked so far; this one is mysql' },
]

function renderIt(canCheck = true) {
  return render(
    <MantineProvider>
      <Notifications />
      <QueryClientProvider client={new QueryClient({ mutationCache, defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })}>
        <LookupPlans workflowId="wf-1" canCheck={canCheck} />
      </QueryClientProvider>
    </MantineProvider>,
  )
}

describe('LookupPlans', () => {
  it('checks on request and shows each lookup with its indexes and what to change', async () => {
    let calls = 0
    server.use(http.post('/api/workflows/wf-1/insights/lookup-plans', () => {
      calls++
      return HttpResponse.json({ nodes })
    }))
    renderIt()
    expect(calls).toBe(0)
    await userEvent.click(screen.getByRole('button', { name: 'Check lookup queries' }))

    const good = (await screen.findByText('Find order')).closest('[data-lookup-node]') as HTMLElement
    expect(within(good).getByText('Uses index so_doc')).toBeInTheDocument()
    expect(within(good).getByText('Good')).toBeInTheDocument()

    const slow = screen.getByText('Delete items').closest('[data-lookup-node]') as HTMLElement
    expect(within(slow).getByText('Can improve')).toBeInTheDocument()
    expect(within(slow).getByText(PROPOSAL)).toBeInTheDocument()
    expect(within(slow).getByRole('button', { name: 'Copy the SQL for this index' })).toBeInTheDocument()

    const skipped = screen.getByText('Counter').closest('[data-lookup-node]') as HTMLElement
    expect(within(skipped).getByText(/only PostgreSQL sources/)).toBeInTheDocument()
    expect(calls).toBe(1)
  })

  it('says so when the flow has no DB Lookup steps', async () => {
    server.use(http.post('/api/workflows/wf-1/insights/lookup-plans', () => HttpResponse.json({ nodes: [] })))
    renderIt()
    await userEvent.click(screen.getByRole('button', { name: 'Check lookup queries' }))
    expect(await screen.findByText(/no DB Lookup steps/)).toBeInTheDocument()
  })

  it('shows why the check failed, once, under the button', async () => {
    server.use(http.post('/api/workflows/wf-1/insights/lookup-plans', () =>
      HttpResponse.json({ error: 'Forbidden: you do not have access to this workflow\'s vhost' }, { status: 403 })))
    renderIt()
    await userEvent.click(screen.getByRole('button', { name: 'Check lookup queries' }))
    expect(await screen.findByText('Could not check the lookup queries')).toBeInTheDocument()
    expect(screen.queryByText('Action failed')).not.toBeInTheDocument()
  })

  it('offers the check to editors only, since it connects to the source databases', () => {
    renderIt(false)
    expect(screen.queryByRole('button', { name: 'Check lookup queries' })).not.toBeInTheDocument()
    expect(screen.getByText(/An editor can check/)).toBeInTheDocument()
  })
})
