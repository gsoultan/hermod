import { render, screen, within } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { describe, it, expect } from 'vitest'
import { server } from '../test/setupTests'
import { DatabaseCards } from '@/pages/workflows/tuning/DatabaseCards'
import type { DatabaseView } from '@/lib/insights'

const etl: DatabaseView = {
  source_id: 'src-etl', source: 'athena_etl_db', learned: 12, limit: 9, running: 10, long: 9,
  fixed: [{ rule: 'learned_share', title: 'Hermod found it gets 12 connections on athena_etl_db', detail: 'It now runs at most 9 long queries at once and keeps 3 free for lookups.' }],
  needs_you: [{ rule: 'long_query', node_id: 'totals', title: 'A SQL step in this flow has run for 40 min', detail: 'Open the step\'s SQL builder and use Speed.' }],
}
const cdc: DatabaseView = { source_id: 'src-cdc', source: 'orders_cdc', learned: 0, limit: 0, running: 1, long: 1, fixed: [], needs_you: [] }

function renderIt() {
  return render(
    <MantineProvider>
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <DatabaseCards workflowId="wf-1" nodeNames={new Map([['totals', "Yesterday's totals"]])} enabled />
      </QueryClientProvider>
    </MantineProvider>,
  )
}

describe('Tuning databases', () => {
  it('shows what Hermod fixed and what needs a person, per database', async () => {
    server.use(http.get('/api/workflows/wf-1/insights/databases', () => HttpResponse.json({ databases: [cdc, etl] })))
    renderIt()

    const panel = (await screen.findByText('athena_etl_db')).closest('[data-database]') as HTMLElement
    expect(within(panel).getByText('Hermod gets 12 connections here, runs up to 9 long queries at once, 10 queries running now.')).toBeInTheDocument()
    expect(within(panel).getByText('Hermod found it gets 12 connections on athena_etl_db')).toBeInTheDocument()
    const long = within(panel).getByText('A SQL step in this flow has run for 40 min').closest('[data-db-card]') as HTMLElement
    expect(within(long).getByText("Step: Yesterday's totals")).toBeInTheDocument()

    const quiet = screen.getByText('orders_cdc').closest('[data-database]') as HTMLElement
    expect(within(quiet).getByText('Nothing needs you here.')).toBeInTheDocument()
    expect(within(quiet).getByText('1 query running now.')).toBeInTheDocument()
  })

  it('says so when the flow reaches no tracked database', async () => {
    server.use(http.get('/api/workflows/wf-1/insights/databases', () => HttpResponse.json({ databases: [] })))
    renderIt()
    expect(await screen.findByText('This flow reaches no PostgreSQL database that Hermod tracks.')).toBeInTheDocument()
  })
})
