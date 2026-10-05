import { render, screen, within, fireEvent, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { http, HttpResponse } from 'msw'
import { describe, it, expect, vi } from 'vitest'
import { server } from '../test/setupTests'
import { SQLQueryBuilder } from '@/components/forms/SQLQueryBuilder'

// The builder was laid out for a wide modal and for a SELECT. Inside an
// execute_sql node it sat in a third of a drawer: the editor was 250px wide,
// the schema list 130px, and the toolbar offered GROUP BY to a statement that
// writes. It is now one workspace -- editor, then Variables / Schema /
// Templates -- that arranges itself by the room it is given and knows whether
// the statement reads or writes.
const columns = [
  { name: 'id', type: 'bigint', is_pk: true, is_identity: true },
  { name: 'code', type: 'text' },
  { name: 'amount', type: 'numeric' },
]

function renderBuilder(props: Record<string, any> = {}) {
  const onQueryChange = vi.fn()
  render(
    <MantineProvider>
      <SQLQueryBuilder
        type="source"
        sourceType="postgres"
        config={{}}
        intent="write"
        initialQuery=""
        onQueryChange={onQueryChange}
        availableFields={[{ path: 'code', type: 'string' }]}
        sampleMessage={{ code: 'C-1' }}
        {...props}
      />
    </MantineProvider>
  )
  return { onQueryChange }
}

const editor = () => screen.getByRole('textbox', { name: 'SQL statement' }) as HTMLTextAreaElement

describe('SQL builder workspace', () => {
  it('starts a write statement empty, not on a SELECT nobody typed', () => {
    renderBuilder()
    expect(editor().value).toBe('')
    expect(screen.getByRole('button', { name: /run statement/i })).toBeInTheDocument()
  })

  it('numbers the lines of the statement', () => {
    renderBuilder({ initialQuery: 'INSERT INTO t (a)\nVALUES (1)\nRETURNING id' })
    const gutter = screen.getByTestId('sql-editor').firstElementChild!
    expect(gutter).toHaveTextContent('123')
  })

  it('offers the keywords a write uses, and not the ones it does not', async () => {
    const user = userEvent.setup()
    renderBuilder()
    await user.click(screen.getByRole('tab', { name: /templates/i }))
    const panel = screen.getByRole('tabpanel', { name: /templates/i })
    expect(within(panel).getByRole('button', { name: 'RETURNING' })).toBeInTheDocument()
    expect(within(panel).queryByRole('button', { name: 'GROUP BY' })).toBeNull()
    // {{.last_value}} is a batch query's resume point; a write has none.
    expect(within(panel).queryByRole('button', { name: '{{.last_value}}' })).toBeNull()
  })

  it('writes a template for the table opened in Schema, and can put the old statement back', async () => {
    server.use(
      http.post('*/api/sources/discover/tables', () => HttpResponse.json(['orders', 'audit'])),
      http.post('*/api/sources/discover/columns', () => HttpResponse.json(columns))
    )
    const user = userEvent.setup()
    const { onQueryChange } = renderBuilder({ initialQuery: 'DELETE FROM old' })

    // Opening the tab reads the schema; nobody should have to find a button first.
    await user.click(screen.getByRole('tab', { name: /schema/i }))
    const schema = screen.getByRole('tabpanel', { name: /schema/i })
    await user.click(await within(schema).findByRole('button', { name: 'orders' }))
    expect(await within(schema).findByText('amount')).toBeInTheDocument()
    expect(within(schema).getByText('key')).toBeInTheDocument()

    await user.click(screen.getByRole('tab', { name: /templates/i }))
    const templates = screen.getByRole('tabpanel', { name: /templates/i })
    const card = within(templates).getByTestId('sql-template-insert-returning')
    expect(card).toHaveTextContent('INSERT INTO orders (code, amount)')
    // It says what the button will do to the statement that is there.
    await user.click(within(card).getByRole('button', { name: /insert and return the row/i }))

    const expected = 'INSERT INTO orders (code, amount)\nVALUES ({{.code}}, {{.amount}})\nRETURNING *'
    expect(editor().value).toBe(expected)
    expect(onQueryChange).toHaveBeenLastCalledWith(expected)

    await user.click(screen.getByRole('button', { name: /undo/i }))
    expect(editor().value).toBe('DELETE FROM old')
    expect(onQueryChange).toHaveBeenLastCalledWith('DELETE FROM old')
  })

  it('says a schema failure is a schema failure', async () => {
    server.use(
      http.post('*/api/sources/discover/tables', () =>
        HttpResponse.json({ error: 'connection refused' }, { status: 500 })
      )
    )
    const user = userEvent.setup()
    renderBuilder()
    await user.click(screen.getByRole('tab', { name: /schema/i }))
    const schema = screen.getByRole('tabpanel', { name: /schema/i })
    expect(await within(schema).findByText(/connection refused/)).toBeInTheDocument()
    expect(screen.queryByText('Query failed')).toBeNull()
  })

  it('runs the statement and shows what it returned', async () => {
    let sent: any
    server.use(
      http.post('*/api/sources/query', async ({ request }) => {
        sent = await request.json()
        return HttpResponse.json([{ id: 7, code: 'C-1' }])
      })
    )
    const user = userEvent.setup()
    renderBuilder({ initialQuery: 'INSERT INTO orders (code) VALUES ({{.code}}) RETURNING id, code' })

    await user.click(screen.getByRole('button', { name: /run statement/i }))

    const results = await screen.findByTestId('sql-results')
    expect(results).toHaveTextContent('1 rows')
    expect(results).toHaveTextContent('Returned rows')
    expect(within(results).getByText('C-1')).toBeInTheDocument()
    expect(sent.query).toContain('RETURNING id, code')
    expect(sent.sampleData).toEqual({ code: 'C-1' })
  })

  it('tells a write that returned nothing apart from a query that found nothing', async () => {
    server.use(http.post('*/api/sources/query', () => HttpResponse.json(null)))
    const user = userEvent.setup()
    renderBuilder({ initialQuery: 'INSERT INTO orders (code) VALUES (1)' })
    await user.click(screen.getByRole('button', { name: /run statement/i }))
    expect(await screen.findByTestId('sql-results')).toHaveTextContent(/ran and returned no rows/i)
  })

  it('opens the same workspace large, with one editor on the page', async () => {
    const user = userEvent.setup()
    renderBuilder({ initialQuery: 'DELETE FROM t' })
    await user.click(screen.getByRole('button', { name: 'Toggle fullscreen editor' }))

    const dialog = await screen.findByRole('dialog', { name: /sql workspace/i })
    await waitFor(() => expect(within(dialog).getByRole('textbox', { name: 'SQL statement' })).toBeInTheDocument())
    expect(screen.getAllByRole('textbox', { name: 'SQL statement' })).toHaveLength(1)
    expect(within(dialog).getByRole('tab', { name: /schema/i })).toBeInTheDocument()

    fireEvent.change(within(dialog).getByRole('textbox', { name: 'SQL statement' }), {
      target: { value: 'DELETE FROM t WHERE id = 1' },
    })
    expect(editor().value).toBe('DELETE FROM t WHERE id = 1')
  })

  it('keeps a read query as it was: its default, its keywords, its button', async () => {
    const user = userEvent.setup()
    renderBuilder({ intent: undefined, initialQuery: undefined })
    expect(editor().value).toBe('SELECT * FROM tables LIMIT 10')
    expect(screen.getByRole('button', { name: /run query/i })).toBeInTheDocument()
    await user.click(screen.getByRole('tab', { name: /templates/i }))
    const panel = screen.getByRole('tabpanel', { name: /templates/i })
    expect(within(panel).getByRole('button', { name: 'GROUP BY' })).toBeInTheDocument()
    expect(within(panel).getByRole('button', { name: '{{.last_value}}' })).toBeInTheDocument()
  })
})
