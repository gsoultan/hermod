import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { Notifications } from '@mantine/notifications'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { useState, type ReactNode } from 'react'
import { server, signInAs } from '../test/setupTests'
import { ConfirmProvider } from '@/components/common/ConfirmProvider'
import { EnvironmentsPage } from '@/pages/environments/EnvironmentsPage'
import {
  filledValues, neededSecrets, parseEnvBlock, SecretValues, whereLine, type NeededSecret,
} from '@/pages/environments/SecretValues'

vi.mock('@tanstack/react-router', () => ({
  useParams: () => ({}),
  Link: ({ children, to }: { children: ReactNode; to: string }) => <a href={to}>{children}</a>,
}))

function wrap(ui: ReactNode) {
  return render(
    <MantineProvider>
      <Notifications />
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })}>
        <ConfirmProvider>{ui}</ConfirmProvider>
      </QueryClientProvider>
    </MantineProvider>,
  )
}

describe('secret value helpers', () => {
  it('lists each secret once, with every flow that uses it', () => {
    expect(neededSecrets([
      { workflow_name: 'refunds', missing_secrets: ['STRIPE_KEY', 'SLACK'] },
      { workflow_name: 'payouts', missing_secrets: ['STRIPE_KEY'] },
      { workflow_name: 'orders', missing_secrets: null },
    ])).toEqual([
      { name: 'SLACK', usedBy: ['refunds'], via: [], places: [] },
      { name: 'STRIPE_KEY', usedBy: ['refunds', 'payouts'], via: [], places: [] },
    ])
  })

  it('names the source or sink that reads a secret', () => {
    expect(neededSecrets([
      { workflow_name: 'refunds', missing_secrets: ['WH_PASSWORD', 'SLACK'], secret_uses: { WH_PASSWORD: ['sink warehouse'] } },
      { workflow_name: 'payouts', missing_secrets: ['WH_PASSWORD'], secret_uses: { WH_PASSWORD: ['sink warehouse'] } },
    ])).toEqual([
      { name: 'SLACK', usedBy: ['refunds'], via: [], places: [] },
      { name: 'WH_PASSWORD', usedBy: ['refunds', 'payouts'], via: ['sink warehouse'], places: [{ kind: 'sink', name: 'warehouse' }] },
    ])
  })

  it('lists every step, source and sink that uses a secret, each once, and which flow a step is in', () => {
    const step = { kind: 'step' as const, name: 'Look up plan price', field: 'Headers › Authorization', node_id: 'n4', value: '{{secret.TOKEN}}' }
    const sink = { kind: 'sink' as const, name: 'warehouse', field: 'Password' }
    expect(neededSecrets([
      { workflow_name: 'orders', workflow_id: 'wf-1', missing_secrets: ['TOKEN'], secret_uses: { TOKEN: ['sink warehouse'] }, secret_places: { TOKEN: [step, sink] } },
      { workflow_name: 'refunds', workflow_id: 'wf-2', missing_secrets: ['TOKEN'], secret_places: { TOKEN: [sink] } },
    ])).toEqual([
      { name: 'TOKEN', usedBy: ['orders', 'refunds'], via: ['sink warehouse'], places: [{ ...step, workflowId: 'wf-1' }, sink] },
    ])
  })

  it('says how many secrets steps and connections use', () => {
    const at = (kind: 'step' | 'source' | 'sink') => [{ kind, name: 'x' }]
    expect(whereLine([
      { name: 'A', usedBy: [], places: at('step') }, { name: 'B', usedBy: [], places: at('step') },
      { name: 'C', usedBy: [], places: at('step') }, { name: 'D', usedBy: [], places: at('sink') },
    ])).toBe('3 are used by steps and 1 by a sink.')
    expect(whereLine([{ name: 'A', usedBy: [], places: at('source') }])).toBe('1 is used by a source.')
    expect(whereLine([{ name: 'A', usedBy: [], places: [] }])).toBe('')
  })

  it('reads a .env block and keeps apart the names that are not needed', () => {
    const got = parseEnvBlock([
      '# billing',
      'export BILLING_DB_PASSWORD="s3cr=t"',
      "SFTP_KEY='abc'",
      '',
      'NOT_NEEDED=1',
      'garbage line',
      'S3_SECRET = spaced ',
    ].join('\n'), ['BILLING_DB_PASSWORD', 'SFTP_KEY', 'S3_SECRET'])
    expect(got.values).toEqual({ BILLING_DB_PASSWORD: 's3cr=t', SFTP_KEY: 'abc', S3_SECRET: 'spaced' })
    expect(got.ignored).toEqual(['NOT_NEEDED'])
  })

  it('sends only what has a value', () => {
    expect(filledValues({ A: 'x', B: '', C: '   ' })).toEqual({ A: 'x' })
  })
})

function Harness({ needed, onValues }: { needed: NeededSecret[]; onValues?: (v: Record<string, string>) => void }) {
  const [values, setValues] = useState<Record<string, string>>({})
  return (
    <SecretValues
      needed={needed}
      values={values}
      onChange={(v) => { setValues(v); onValues?.(v) }}
      where="Production"
      mode="send"
    />
  )
}

describe('typing secret values for production', () => {
  it('asks for a few right in place, never showing them', async () => {
    let last: Record<string, string> = {}
    const user = userEvent.setup()
    wrap(<Harness needed={[{ name: 'SLACK_TOKEN', usedBy: ['orders'], places: [] }, { name: 'WAREHOUSE_PASSWORD', usedBy: ['orders'], places: [] }]} onValues={(v) => { last = v }} />)
    expect(screen.getByText('2 secrets are new to Production')).toBeInTheDocument()
    expect(screen.getByText(/never stored or shown here/)).toBeInTheDocument()
    const box = screen.getByLabelText('WAREHOUSE_PASSWORD')
    expect(box).toHaveAttribute('type', 'password')
    await user.type(box, 'hunter2')
    expect(last).toEqual({ WAREHOUSE_PASSWORD: 'hunter2' })
    expect(screen.getByText(/the approver fills the rest/)).toBeInTheDocument()
  })

  it('says which step, source or sink uses a secret, and in which setting', () => {
    wrap(<Harness needed={[
      { name: 'REDIS_PASSWORD', usedBy: ['orders'], places: [{ kind: 'step', name: 'Cache totals', field: 'Password' }] },
      { name: 'WH_PASSWORD', usedBy: ['orders'], via: ['sink warehouse'], places: [{ kind: 'sink', name: 'warehouse', field: 'Password' }] },
    ]} />)
    expect(screen.getByText(/^1 is used by a step and 1 by a sink\./)).toBeInTheDocument()
    expect(screen.getByText('Step')).toBeInTheDocument()
    expect(screen.getByText('Cache totals')).toBeInTheDocument()
    expect(screen.getByText('Sink')).toBeInTheDocument()
    expect(screen.getByText('warehouse')).toBeInTheDocument()
    expect(screen.getAllByText('· Password')).toHaveLength(2)
  })

  it('shows how a step uses a secret, and opens the step', async () => {
    const user = userEvent.setup()
    wrap(<Harness needed={[{
      name: 'PRICING_TOKEN', usedBy: ['orders'],
      places: [{ kind: 'step', name: 'Look up plan price', field: 'Headers › Authorization', node_id: 'n4', value: '{{concat("Bearer ", secret.PRICING_TOKEN)}}', workflowId: 'wf-1' }],
    }]} />)
    await user.click(screen.getByRole('button', { name: 'Where PRICING_TOKEN is used in Look up plan price' }))
    const mark = await screen.findByText('PRICING_TOKEN', { selector: 'mark' })
    expect(mark).toBeInTheDocument()
    // jsdom gives the floating panel no layout, so it is read by its text, not its role.
    expect((await screen.findByText(/Open step/)).closest('a')).toHaveAttribute('href', '/workflows/wf-1/edit')
  })

  it('shows two places and the rest on request', async () => {
    const user = userEvent.setup()
    const places = ['one', 'two', 'three', 'four'].map((name) => ({ kind: 'step' as const, name }))
    wrap(<Harness needed={[{ name: 'KEY', usedBy: ['orders'], places }]} />)
    expect(screen.queryByText('three')).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'and 2 more' }))
    expect(screen.getByText('four')).toBeInTheDocument()
  })

  it('fills many in one place: search, Empty, and a pasted .env block', async () => {
    const needed = Array.from({ length: 14 }, (_, i) => ({ name: `SECRET_${String(i).padStart(2, '0')}`, usedBy: i % 2 ? ['payouts'] : ['refunds', 'payouts'],
      places: [{ kind: i % 3 ? 'sink' as const : 'step' as const, name: i % 3 ? 'warehouse' : `Step ${i}` }] }))
    let last: Record<string, string> = {}
    const user = userEvent.setup()
    wrap(<Harness needed={needed} onValues={(v) => { last = v }} />)

    expect(screen.getByText('14 secrets are new to Production')).toBeInTheDocument()
    expect(screen.getByText('0 of 14 filled')).toBeInTheDocument()
    expect(screen.queryByLabelText('SECRET_00')).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Fill in values' }))
    const dialog = await screen.findByRole('dialog', { name: 'Secret values for Production' })
    expect(within(dialog).getByText('Empty (14)')).toBeInTheDocument()

    await user.click(within(dialog).getByRole('button', { name: 'Paste a .env block' }))
    await user.type(within(dialog).getByLabelText(/Lines like NAME=value/), 'SECRET_00=a{enter}SECRET_01=b{enter}OTHER=c')
    await user.click(within(dialog).getByRole('button', { name: 'Paste' }))
    expect(last).toEqual({ SECRET_00: 'a', SECRET_01: 'b' })
    expect(within(dialog).getByText('Not needed here, so ignored: OTHER')).toBeInTheDocument()
    expect(within(dialog).getByText('Empty (12)')).toBeInTheDocument()
    expect(within(dialog).getByRole('button', { name: 'Done · 2 of 14 filled' })).toBeInTheDocument()

    await user.type(within(dialog).getByRole('textbox', { name: 'Search secrets' }), 'SECRET_1')
    expect(within(dialog).getAllByLabelText(/^SECRET_1/)).toHaveLength(4)
    expect(within(dialog).queryByLabelText('SECRET_02')).not.toBeInTheDocument()

    await user.click(within(dialog).getByRole('button', { name: 'Clear all' }))
    expect(last).toEqual({})
    await user.click(within(dialog).getByRole('button', { name: 'Done · 0 of 14 filled' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  }, 20000)

  it('filters many by where they are used', async () => {
    const needed = Array.from({ length: 8 }, (_, i) => ({
      name: `SECRET_${i}`, usedBy: ['payouts'],
      places: [{ kind: i % 2 ? 'sink' as const : 'step' as const, name: i % 2 ? 'warehouse' : `Step ${i}` }],
    }))
    const user = userEvent.setup()
    wrap(<Harness needed={needed} />)
    await user.click(screen.getByRole('button', { name: 'Fill in values' }))
    const dialog = await screen.findByRole('dialog', { name: 'Secret values for Production' })
    const where = within(dialog).getByRole('group', { name: 'Where it is used' })
    expect(within(where).queryByText(/^Sources/)).not.toBeInTheDocument()
    await user.click(within(where).getByText('Steps (4)'))
    expect(within(dialog).getAllByLabelText(/^SECRET_/)).toHaveLength(4)
    expect(within(dialog).queryByLabelText('SECRET_1')).not.toBeInTheDocument()
    await user.click(within(where).getByText('Anywhere'))
    expect(within(dialog).getAllByLabelText(/^SECRET_/)).toHaveLength(8)
  })
})

const promotion = (overrides: Record<string, unknown> = {}) => ({
  id: 'pr-1', peer_id: 'p-1', from_env: 'Staging', workflow_id: 'wf-1', workflow_name: 'orders', vhost: 'hr',
  from_version: 3, sender: 'ada', start: true, auto_rollback: true, changes: [], status: 'pending',
  created_at: '2026-10-27T10:00:00Z', ...overrides,
})

describe('approving on production', () => {
  beforeEach(() => {
    signInAs('Administrator', 'root')
    server.use(
      http.get('/api/environments/self', () => HttpResponse.json({ name: 'Prod', role: 'production' })),
      http.get('/api/environments/rules', () => HttpResponse.json({ approval: 'admin_except_sender', auto_rollback: true, min_soak_minutes: 0 })),
      http.get('/api/environments/peers', () => HttpResponse.json([])),
    )
  })

  it('asks only for what the sender left empty, and sends what was typed', async () => {
    const bodies: unknown[] = []
    server.use(
      http.get('/api/environments/promotions', () => HttpResponse.json([
        promotion({ secrets: ['CRM_KEY', 'SLACK_TOKEN'], secrets_filled: ['CRM_KEY'] }),
      ])),
      http.post('/api/environments/promotions/:id/approve', async ({ request }) => {
        bodies.push(await request.text())
        return HttpResponse.json(promotion({ status: 'deployed' }))
      }),
    )
    const user = userEvent.setup()
    wrap(<EnvironmentsPage />)

    expect(await screen.findByText('ada typed values for CRM_KEY.')).toBeInTheDocument()
    expect(screen.getByText('1 secret has no value in hr yet')).toBeInTheDocument()
    expect(screen.queryByLabelText('CRM_KEY')).not.toBeInTheDocument()
    await user.type(screen.getByLabelText('SLACK_TOKEN'), 'xoxb-1')
    await user.click(screen.getByRole('button', { name: 'Approve and go live' }))
    await user.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Approve and go live' }))
    await waitFor(() => expect(bodies).toEqual([JSON.stringify({ secrets: { SLACK_TOKEN: 'xoxb-1' } })]))
  })

  it('approves with no body when nothing was typed, and says afterwards what is still missing', async () => {
    const bodies: string[] = []
    let status = 'pending'
    server.use(
      http.get('/api/environments/promotions', () => HttpResponse.json([
        status === 'pending'
          ? promotion({ secrets: ['SLACK_TOKEN'] })
          : promotion({ status, secrets: ['SLACK_TOKEN', 'CRM_KEY'], secrets_copied: ['CRM_KEY'], secrets_missing: ['SLACK_TOKEN'] }),
      ])),
      http.post('/api/environments/promotions/:id/approve', async ({ request }) => {
        bodies.push(await request.text())
        status = 'deployed'
        return HttpResponse.json(promotion({ status }))
      }),
    )
    const user = userEvent.setup()
    wrap(<EnvironmentsPage />)
    await user.click(await screen.findByRole('button', { name: 'Approve and go live' }))
    await user.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Approve and go live' }))
    expect(await screen.findByText('Saved secrets CRM_KEY')).toBeInTheDocument()
    expect(screen.getByText('Still without a value: SLACK_TOKEN. Add them on the Secrets page.')).toBeInTheDocument()
    expect(bodies).toEqual([''])
  })

  it('keeps a flow with an empty secret stopped, unless the approver starts it anyway', async () => {
    const bodies: string[] = []
    server.use(
      http.get('/api/environments/promotions', () => HttpResponse.json([promotion({ secrets: ['SLACK_TOKEN'] })])),
      http.post('/api/environments/promotions/:id/approve', async ({ request }) => {
        bodies.push(await request.text())
        return HttpResponse.json(promotion({ status: 'deployed' }))
      }),
    )
    const user = userEvent.setup()
    wrap(<EnvironmentsPage />)

    const stopped = await screen.findByRole('radio', { name: /Keep it stopped until SLACK_TOKEN has a value/ })
    expect(stopped).toBeChecked()
    await user.click(screen.getByRole('button', { name: 'Approve and go live' }))
    const dialog = within(await screen.findByRole('dialog'))
    expect(dialog.getByText(/stays stopped until SLACK_TOKEN has a value/)).toBeInTheDocument()
    await user.click(dialog.getByRole('button', { name: 'Approve and go live' }))
    await waitFor(() => expect(bodies).toEqual(['']))

    await user.click(screen.getByRole('radio', { name: /Start it anyway/ }))
    await user.click(screen.getByRole('button', { name: 'Approve and go live' }))
    await user.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Approve and go live' }))
    await waitFor(() => expect(bodies).toEqual(['', JSON.stringify({ start_anyway: true })]))
  })

  it('offers no choice once every secret has a value', async () => {
    server.use(
      http.get('/api/environments/promotions', () => HttpResponse.json([promotion({ secrets: ['SLACK_TOKEN'] })])),
    )
    const user = userEvent.setup()
    wrap(<EnvironmentsPage />)
    await user.type(await screen.findByLabelText('SLACK_TOKEN'), 'xoxb-1')
    expect(screen.queryByRole('radio', { name: /Keep it stopped/ })).not.toBeInTheDocument()
  })

  it('names the connections approving creates here', async () => {
    server.use(
      http.get('/api/environments/promotions', () => HttpResponse.json([promotion({
        connections: [{ kind: 'sink', id: 'k-slack', name: 'slack-alerts', type: 'slack' }, { kind: 'source', id: 's-crm', name: 'crm', type: 'mysql' }],
      })])),
    )
    wrap(<EnvironmentsPage />)
    expect(await screen.findByText('Approving creates sink slack-alerts and source crm in hr.')).toBeInTheDocument()
  })

  it('says a flow was kept stopped for a missing secret', async () => {
    server.use(
      http.get('/api/environments/promotions', () => HttpResponse.json([
        promotion({ status: 'deployed', secrets: ['SLACK_TOKEN'], secrets_missing: ['SLACK_TOKEN'], kept_stopped: true }),
      ])),
    )
    wrap(<EnvironmentsPage />)
    expect(await screen.findByText('Kept stopped until SLACK_TOKEN has a value. Start it from the flow once it is set.')).toBeInTheDocument()
  })
})
