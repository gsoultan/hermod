import { useState } from 'react'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { describe, it, expect } from 'vitest'
import { http, HttpResponse } from 'msw'
import { server } from '../test/setupTests'
import { TxGroupSinkConfig } from '@/components/workflow/Sink/TxGroupSinkConfig'

const STORED_SINKS = [
  { id: 'pg1', name: 'Orders', type: 'postgres' },
  { id: 'my1', name: 'Billing', type: 'mysql' },
  { id: 'k1', name: 'Events', type: 'kafka' },
  { id: 'grp', name: 'Old group', type: 'txgroup' },
]

function serve({ logReady = true, logProblem = '' } = {}) {
  server.use(
    http.get('/api/sinks/capabilities/two-phase', () =>
      HttpResponse.json({
        types: ['postgres', 'yugabyte', 'mysql', 'mariadb'],
        log_ready: logReady,
        log_problem: logProblem,
      }),
    ),
    http.get('/api/sinks', () => HttpResponse.json({ data: STORED_SINKS, total: STORED_SINKS.length })),
  )
}

function Harness({ initial, sinks }: { initial: Record<string, string>; sinks?: any[] }) {
  const [config, setConfig] = useState<Record<string, any>>(initial)
  return (
    <TxGroupSinkConfig
      config={config}
      sinks={sinks}
      updateConfig={(key, value) => setConfig((c) => ({ ...c, [key]: value }))}
    />
  )
}

const ui = (initial: Record<string, string> = {}, sinks?: any[]) =>
  render(
    <MantineProvider>
      <Harness initial={initial} sinks={sinks} />
    </MantineProvider>,
  )

/**
 * The Sinks page and the workflow editor both open this form, and neither
 * handed it the list of sinks. So the member picker said "No eligible sinks"
 * and every saved member was reported as deleted: a group could not be set up
 * from the UI at all. The form now asks for the list itself.
 */
describe('the transactional group form finds its own members', () => {
  it('offers the stored sinks that can take part, by name, with nothing passed in', async () => {
    serve()
    ui()
    expect((await screen.findAllByText(/sinks to write to/i)).length).toBeGreaterThan(0)
    const picker = await screen.findByPlaceholderText(/choose two or more/i)
    await userEvent.setup().click(picker)
    expect(await screen.findByRole('option', { name: /Orders/, hidden: true })).toBeInTheDocument()
    expect(screen.getByRole('option', { name: /Billing/, hidden: true })).toBeInTheDocument()
    expect(screen.queryByRole('option', { name: /Events/, hidden: true })).not.toBeInTheDocument()
    expect(screen.queryByRole('option', { name: /Old group/, hidden: true })).not.toBeInTheDocument()
  })

  it('does not call saved members missing while the list is still loading or once it has loaded', async () => {
    serve()
    ui({ members: 'pg1,my1' })
    await screen.findByText(/2 sinks selected/i)
    expect(screen.queryByText(/no longer exist/i)).not.toBeInTheDocument()
  })
})

/**
 * Out of the box Hermod has no state store, so every group is refused when its
 * workflow starts. The form says so before then, with the fix one click away.
 */
describe('the transactional group form says whether it can run', () => {
  it('names a missing transaction log and links to where it is set', async () => {
    serve({ logReady: false, logProblem: 'a state store is required so the coordinator\'s transaction log survives a restart' })
    ui({ members: 'pg1,my1' })
    expect(await screen.findByText(/state store is required/i)).toBeInTheDocument()
    const link = screen.getByRole('link', { name: /open settings/i })
    expect(link).toHaveAttribute('href', '/settings?tab=platform')
  })

  it('says it is ready when the log is ready and two members are chosen', async () => {
    serve()
    ui({ members: 'pg1,my1' })
    expect(await screen.findByText(/ready to run/i)).toBeInTheDocument()
  })

  it('asks for a second member when only one is chosen', async () => {
    serve()
    ui({ members: 'pg1' })
    expect(await screen.findByText(/choose at least two/i)).toBeInTheDocument()
  })
})

describe('the transactional group form keeps the rare settings out of the way', () => {
  it('folds the prepared-age setting under Advanced', async () => {
    serve()
    ui({ members: 'pg1,my1' })
    await screen.findByText(/2 sinks selected/i)
    expect(screen.queryByLabelText(/give up on a stuck transaction after/i)).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /advanced/i }))
    expect(await screen.findByLabelText(/give up on a stuck transaction after/i)).toBeInTheDocument()
  })

  it('shows database setup only for the kinds of database chosen', async () => {
    serve()
    ui({ members: 'pg1' })
    fireEvent.click(await screen.findByRole('button', { name: /database setup/i }))
    await waitFor(() => expect(screen.getByText(/max_prepared_transactions/)).toBeInTheDocument())
    expect(screen.queryByText(/XA_RECOVER_ADMIN/)).not.toBeInTheDocument()
  })
})
