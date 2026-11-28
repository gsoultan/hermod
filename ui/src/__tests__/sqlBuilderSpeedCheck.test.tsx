import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { http, HttpResponse } from 'msw'
import { describe, it, expect } from 'vitest'
import { server } from '../test/setupTests'
import { SQLQueryBuilder } from '@/components/forms/SQLQueryBuilder'
import type { SpeedCheck } from '@/lib/speedCheck'

const QUERY = "SELECT * FROM sales_line l WHERE date_trunc('day', l.created_at) = CURRENT_DATE - 1"
const REWRITTEN = "SELECT * FROM sales_line l WHERE (l.created_at >= (CURRENT_DATE - 1) AND l.created_at < (CURRENT_DATE - 1) + interval '1 day')"

const answer: SpeedCheck = {
  sql: QUERY, scans: [], tables: [], cost: 98_000, rewritten: REWRITTEN, rewritten_cost: 450,
  findings: [{
    rule: 'wrapped_column', severity: 'warn', table: 'public.sales_line',
    title: 'A function on the column stops index use on public.sales_line',
    detail: 'The index sales_line_created_idx on created_at cannot be used for that.',
    rewrite: "l.created_at >= (CURRENT_DATE - 1) AND l.created_at < (CURRENT_DATE - 1) + interval '1 day'",
  }],
}

function renderBuilder(sourceType = 'postgres') {
  return render(
    <MantineProvider>
      <SQLQueryBuilder type="source" sourceType={sourceType} config={{ type: sourceType, config: { host: 'db' } }} initialQuery={QUERY} />
    </MantineProvider>,
  )
}

describe('SQL builder speed check', () => {
  it('plans the statement, says how much less work the rewrite is, and applies it', async () => {
    let sent: any = null
    server.use(http.post('/api/sources/query/speed-check', async ({ request }) => {
      sent = await request.json()
      return HttpResponse.json(answer)
    }))
    renderBuilder()
    await userEvent.click(screen.getByRole('button', { name: 'Check speed' }))

    expect(await screen.findByText('A faster way to write it')).toBeInTheDocument()
    expect(sent).toEqual({ config: { type: 'postgres', config: { type: 'postgres', config: { host: 'db' } } }, query: QUERY })
    expect(screen.getByText(/about 217 times less work/)).toBeInTheDocument()
    expect(screen.getByText('Can improve')).toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: 'Apply to the query' }))
    expect(screen.getByLabelText('SQL statement')).toHaveValue(REWRITTEN)
    expect(screen.getByText('The speed check replaced your statement.')).toBeInTheDocument()
    expect(screen.getByText('The statement changed since this check.')).toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: 'Undo' }))
    expect(screen.getByLabelText('SQL statement')).toHaveValue(QUERY)
  })

  it('shows why the statement could not be checked', async () => {
    server.use(http.post('/api/sources/query/speed-check', () =>
      HttpResponse.json({ error: 'the query could not be planned: relation "sales_line" does not exist' }, { status: 422 })))
    renderBuilder()
    await userEvent.click(screen.getByRole('button', { name: 'Check speed' }))
    expect(await screen.findByText('Could not check the statement')).toBeInTheDocument()
    expect(screen.getByText(/relation "sales_line" does not exist/)).toBeInTheDocument()
  })

  it('says it reads PostgreSQL plans when the database is another', async () => {
    renderBuilder('mysql')
    await userEvent.click(screen.getByRole('tab', { name: 'Speed' }))
    expect(screen.getByText(/reads PostgreSQL query plans, and this database is mysql/)).toBeInTheDocument()
  })
})
