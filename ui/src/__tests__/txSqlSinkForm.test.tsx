import { useState } from 'react'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { describe, it, expect } from 'vitest'
import { http, HttpResponse } from 'msw'
import { server } from '../test/setupTests'
import { TxSqlSinkConfig } from '@/components/workflow/Sink/TxSqlSinkConfig'

let latest: Record<string, any> = {}

function Harness({ initial, incomingPayload }: { initial: Record<string, any>; incomingPayload?: any }) {
  const [config, setConfig] = useState<Record<string, any>>(initial)
  return (
    <TxSqlSinkConfig
      config={config}
      vhost="/"
      incomingPayload={incomingPayload}
      updateConfig={(key, value) =>
        setConfig((c) => {
          const next = { ...c, [key]: value }
          latest = next
          return next
        })
      }
    />
  )
}

const ui = (initial: Record<string, any> = {}, incomingPayload?: any) => {
  latest = { ...initial }
  return render(
    <MantineProvider>
      <Harness initial={initial} incomingPayload={incomingPayload} />
    </MantineProvider>,
  )
}

const parentChild = JSON.stringify([
  { name: 'order', sql: 'INSERT INTO orders (total) VALUES ({{total}}) ON CONFLICT (external_id) DO UPDATE SET total = EXCLUDED.total RETURNING id' },
  { name: 'audit', sql: 'UPDATE customers SET last_order_id = {{order.id}} WHERE id = {{customer}}' },
])

describe('the transactional SQL form picks the right mode', () => {
  it('starts a new sink in Guided mode', () => {
    ui({ driver: 'postgres' })
    expect(screen.getByRole('radio', { name: 'Guided' })).toBeChecked()
    expect(screen.getByRole('button', { name: /add step/i })).toBeInTheDocument()
  })

  // Sinks saved before Guided mode existed have statements and no mode. They
  // must keep opening as the SQL they are.
  it('opens a sink saved with statements and no mode in SQL mode', () => {
    ui({ driver: 'postgres', statements: parentChild })
    expect(screen.getByRole('radio', { name: 'SQL' })).toBeChecked()
    expect(screen.getAllByRole('textbox', { name: 'SQL' })).toHaveLength(2)
  })
})

describe('the SQL mode statement list', () => {
  it('stores what is typed as the statement list', () => {
    ui({ driver: 'postgres', mode: 'sql' })
    fireEvent.click(screen.getByRole('button', { name: /add statement/i }))
    fireEvent.change(screen.getByRole('textbox', { name: 'SQL' }), { target: { value: 'SELECT 1' } })
    expect(JSON.parse(latest.statements)).toEqual([{ name: 'step1', sql: 'SELECT 1' }])
  })

  it('says beside a statement that it uses one that runs later', () => {
    ui({
      driver: 'postgres',
      mode: 'sql',
      statements: JSON.stringify([
        { name: 'a', sql: 'UPDATE t SET x = {{b.id}}' },
        { name: 'b', sql: 'INSERT INTO t (y) VALUES (1) ON CONFLICT (y) DO UPDATE SET y = EXCLUDED.y RETURNING id' },
      ]),
    })
    expect(screen.getByText(/runs later; move it below "b"/)).toBeInTheDocument()
  })

  it('offers the columns earlier statements give back', () => {
    ui({ driver: 'postgres', statements: parentChild })
    expect(screen.getByText('{{order.id}}')).toBeInTheDocument()
  })

  it('warns about DO NOTHING with RETURNING', () => {
    ui({ driver: 'postgres', mode: 'sql', statements: JSON.stringify([{ name: 'a', sql: 'INSERT INTO t (a) VALUES (1) ON CONFLICT DO NOTHING RETURNING id' }]) })
    expect(screen.getByText(/returns no row when the row already exists/)).toBeInTheDocument()
  })
})

const guidedSteps = JSON.stringify([
  { name: 'order', table: 'orders', action: 'upsert', match: ['external_id'], columns: [{ column: 'external_id', value: '{{id}}' }], returning: ['id'] },
])

describe('Guided mode', () => {
  it('shows the SQL the server compiles the steps to', async () => {
    server.use(
      http.post('/api/sinks/txsql/compile', () =>
        HttpResponse.json({ statements: [{ name: 'order', sql: 'INSERT INTO "orders" ("external_id") VALUES ({{id}}) ON CONFLICT ("external_id") DO UPDATE SET "external_id" = EXCLUDED."external_id" RETURNING "id"' }] }),
      ),
    )
    ui({ driver: 'postgres', mode: 'guided', steps: guidedSteps })
    fireEvent.click(screen.getByRole('button', { name: /show sql/i }))
    expect(await screen.findByText(/ON CONFLICT \("external_id"\)/)).toBeInTheDocument()
  })

  it('converts to SQL mode only after saying it is one-way', async () => {
    server.use(
      http.post('/api/sinks/txsql/compile', () => HttpResponse.json({ statements: [{ name: 'order', sql: 'INSERT INTO "orders" ("external_id") VALUES ({{id}})' }] })),
    )
    ui({ driver: 'postgres', mode: 'guided', steps: guidedSteps })
    fireEvent.click(screen.getByRole('button', { name: /convert to sql/i }))
    expect(screen.getByText(/cannot be turned back into Guided steps/i)).toBeInTheDocument()
    expect(latest.mode).toBe('guided')
    fireEvent.click(screen.getByRole('button', { name: /convert and edit as sql/i }))
    await waitFor(() => expect(latest.mode).toBe('sql'))
    expect(JSON.parse(latest.statements)[0].name).toBe('order')
  })

  it('says what a step still needs', () => {
    ui({ driver: 'postgres', mode: 'guided', steps: JSON.stringify([{ name: 'order', table: '', action: 'upsert', match: [], columns: [], returning: [] }]) })
    expect(screen.getByText('Pick a table.')).toBeInTheDocument()
  })
})

describe('the Test button', () => {
  it('runs the statements with the sample and shows what each did', async () => {
    let sent: any
    server.use(
      http.post('/api/sinks/txsql/test', async ({ request }) => {
        sent = await request.json()
        return HttpResponse.json({
          simulation: {
            rolled_back: true,
            notes: ['Nothing was committed.'],
            steps: [
              { name: 'order', sql: 'INSERT ... RETURNING id', args: [120], rows: [{ id: 7 }], row_count: 1, affected: -1, duration_ms: 2 },
              { name: 'audit', sql: 'UPDATE ...', args: [7, 'C-9'], row_count: 0, affected: 1, duration_ms: 1 },
            ],
          },
        })
      }),
    )
    ui({ driver: 'postgres', statements: parentChild }, { total: 120, customer: 'C-9' })
    fireEvent.click(screen.getByRole('button', { name: /^test$/i }))
    expect(await screen.findByText(/rolled back/i)).toBeInTheDocument()
    expect(screen.getByText(/returned 1 row/)).toBeInTheDocument()
    expect(screen.getByText(/changed 1 row/)).toBeInTheDocument()
    expect(sent.sample).toEqual({ total: 120, customer: 'C-9' })
    expect(sent.vhost).toBe('/')
  })

  it('shows why the run would fail', async () => {
    server.use(
      http.post('/api/sinks/txsql/test', () =>
        HttpResponse.json({ simulation: { rolled_back: true, error: 'statement "audit": expected at least 1 row', steps: [{ name: 'audit', error: 'expected at least 1 row', affected: 0, row_count: 0 }] } }),
      ),
    )
    ui({ driver: 'postgres', statements: parentChild })
    fireEvent.click(screen.getByRole('button', { name: /^test$/i }))
    expect(await screen.findByText(/would fail/i)).toBeInTheDocument()
  })

  it('shows a refusal from the server', async () => {
    server.use(http.post('/api/sinks/txsql/test', () => HttpResponse.json({ error: 'txsql: no connection details' }, { status: 400 })))
    ui({ driver: 'postgres', statements: parentChild })
    fireEvent.click(screen.getByRole('button', { name: /^test$/i }))
    expect(await screen.findByText(/no connection details/)).toBeInTheDocument()
  })
})

describe('the form on Oracle', () => {
  it('offers Oracle, with its port and a service name', () => {
    ui({ driver: 'oracle', mode: 'sql' })
    expect(screen.getByRole('textbox', { name: 'Port' })).toHaveAttribute('placeholder', '1521')
    expect(screen.getByRole('textbox', { name: 'Service name' })).toBeInTheDocument()
  })

  it('offers only the isolation levels Oracle has', async () => {
    ui({ driver: 'oracle', mode: 'sql' })
    fireEvent.click(screen.getByRole('button', { name: /advanced/i }))
    fireEvent.click((await screen.findAllByLabelText('Isolation'))[0])
    expect(await screen.findByRole('option', { name: 'Serializable', hidden: true })).toBeInTheDocument()
    expect(screen.queryByRole('option', { name: 'Repeatable read', hidden: true })).not.toBeInTheDocument()
  })
})
