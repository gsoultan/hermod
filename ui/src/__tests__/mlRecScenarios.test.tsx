import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { useState, type ReactNode } from 'react'
import { describe, expect, it } from 'vitest'
import { server, signInAs } from '../test/setupTests'
import { TrainModal } from '@/pages/ml/TrainModal'
import { ModelTestModal } from '@/pages/ml/ModelTestModal'
import { MonitoringModal } from '@/pages/ml/MonitoringModal'
import { DatasetsPanel } from '@/pages/ml/DatasetsPanel'
import { MLPredictConfig } from '@/components/workflow/Transformation/configs/ml/MLPredictConfig'
import { MLRecommendBuildConfig } from '@/components/workflow/Transformation/configs/ml/MLRecommendBuildConfig'
import { resolveConfigComponent } from '@/components/workflow/Transformation/configs/registry'
import { canvasNodeTypes } from '@/pages/workflows/WorkflowEditor/components/FlowCanvas'
import { detailNodeTypes } from '@/pages/workflows/WorkflowEditor/components/DetailFlowCanvas'
import { NODE_CATEGORIES } from '@/pages/workflows/WorkflowEditor/constants/nodeCategories'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'
import { ConfirmProvider } from '@/components/common/ConfirmProvider'
import type { MLModel } from '@/lib/mlModels'
import {
  csvTemplate, guessColumns, rulesFromChoices, defaultRuleChoices, scenarioRoles, type RecommendData, type Scenario,
} from '@/lib/mlRecommend'
import { RulesStep } from '@/pages/ml/recommendSteps'
import { scenarioHeadline } from '@/pages/ml/scenarioResult'

/**
 * Recommendation scenarios (phase 9): five cards, the data each needs with
 * examples and templates, column guesses the person confirms, a data health
 * check that can stop training, rules written as sentences, and a result in
 * words with a salesman's plan. A server without the scenario routes falls
 * back to the one-table recommender.
 */

const col = (name: string, type: 'number' | 'string' | 'bool' = 'string') => ({ name, type })
const ds = (name: string, columns: ReturnType<typeof col>[], sample?: Record<string, unknown>[]) =>
  ({ name, rows: 5000, updated_at: '2026-10-10T00:00:00Z', columns, sample })

const DATASETS = {
  orders: ds('orders', [col('cust_id'), col('sku'), col('order_date'), col('qty', 'number'), col('total', 'number')]),
  customers: ds('customers', [col('cust_id'), col('name'), col('rep'), col('region'), col('credit_hold', 'bool'),
    col('lat', 'number'), col('lon', 'number')]),
  items: ds('items', [col('sku'), col('cat'), col('price', 'number'), col('margin', 'number'), col('active', 'bool')]),
  visits: ds('visits', [col('cust_id'), col('visit_date'), col('rep'), col('result')]),
}

const item = (id: string, rank: number, text: string, extra: Record<string, unknown> = {}) =>
  ({ id, score: 1 - rank / 10, rank, reasons: [{ code: 'reorder_due', text }], fields: { name: `Shop ${id}` }, ...extra })

const visitsVersion = {
  model: 'plan', version: '1', task: 'recommendation', algorithm: 'auto', dataset: 'orders', target: '', features: [],
  scenario: 'visits',
  roles: { transactions: { dataset: 'orders', customer: 'cust_id', date: 'order_date' } },
  metrics: { 'ordered_in_period@k': 0.62, 'revenue_share@k': 0.71 },
  baseline: { 'ordered_in_period@k': 0.48 },
  baselines_named: [
    { name: 'most_overdue', metrics: { 'ordered_in_period@k': 0.48, 'revenue_share@k': 0.55 } },
    { name: 'biggest', metrics: { 'ordered_in_period@k': 0.4 } },
  ],
  health: {
    ok: true,
    checks: [{ code: 'short_history', status: 'amber', message: '7 months of orders', fix: 'Add older orders' }],
    stats: { customers: 1240, products: 310, transactions: 52000, months_of_history: 7, tx_per_customer: 41.9 },
  },
  samples: [item('c-7', 1, 'Usually orders every 30 days; last order 41 days ago')],
  rows: { train: 4800, test: 200 }, created_at: '2026-10-10T00:00:00Z',
}

const productsVersion = {
  ...visitsVersion, model: 'offers', scenario: 'products',
  metrics: { 'hit_rate@10': 0.34, 'ndcg@10': 0.2, coverage: 0.4 },
  baseline: { 'hit_rate@10': 0.12 },
  baselines_named: [{ name: 'popular', metrics: { 'hit_rate@10': 0.12 } }],
}

const okCheck = {
  ok: true,
  checks: [
    { code: 'few_customers', status: 'green', message: 'Enough customers', fix: '' },
    { code: 'short_history', status: 'amber', message: 'You have 7 months of orders', fix: 'Planning for 3 months needs at least 6: add older orders if you have them.' },
  ],
  stats: { customers: 1240, products: 310, transactions: 52000, months_of_history: 7, tx_per_customer: 41.9 },
}

const plan = {
  recommendation_id: 'rec_1',
  results: [{
    for: 'rep7',
    items: [],
    plan: [
      { date: '2026-10-13', items: [item('c-7', 1, 'Usually orders every 30 days; last order 41 days ago')] },
      { date: '2026-10-14', items: [item('c-9', 2, 'Not visited for 45 days (target every 30)')] },
    ],
  }],
}

type Calls = Array<{ path: string; body: any }>

function api(opts: { check?: unknown; scenarios?: boolean; version?: unknown; recommend404?: boolean } = {}) {
  const calls: Calls = []
  const scenarios = opts.scenarios ?? true
  const version = opts.version ?? visitsVersion
  server.use(
    http.get('/api/ml/worker', () => HttpResponse.json({ configured: true, ready: true })),
    http.get('/api/vhosts/:vhost/ml/datasets', () => HttpResponse.json({ data: Object.values(DATASETS), total: 4 })),
    http.get('/api/vhosts/:vhost/ml/datasets/:name', ({ params }) =>
      HttpResponse.json({
        ...DATASETS[params.name as keyof typeof DATASETS],
        refresh: { schedule: '0 5 * * *', last_run: '2026-10-11T05:00:00Z', last_error: 'connection refused', next_run: '2026-10-12T05:00:00Z' },
      })),
    http.get('/api/vhosts/:vhost/ml/models', () => HttpResponse.json({ data: [], total: 0 })),
    http.get('/api/vhosts/:vhost/ml/models/:name/versions', () => HttpResponse.json({ data: [version], total: 1 })),
    http.get('/api/sources', () => HttpResponse.json({ data: [{ id: 'pg1', name: 'warehouse', type: 'postgres' }] })),
    http.post('/api/vhosts/:vhost/ml/recommend-check', async ({ request }) => {
      if (!scenarios) return HttpResponse.json({ error: 'not found' }, { status: 404 })
      const body = await request.json()
      calls.push({ path: 'check', body })
      if (!(body as any)?.data) return HttpResponse.json({ error: 'data is required' }, { status: 400 })
      return HttpResponse.json(opts.check ?? okCheck)
    }),
    http.post('/api/vhosts/:vhost/ml/models/:name/train', async ({ params, request }) => {
      calls.push({ path: `train ${params.name}`, body: await request.json() })
      return HttpResponse.json({ model: { name: params.name, task: 'recommendation' }, version, live: true, reason: '' })
    }),
    http.post('/api/vhosts/:vhost/ml/models/:name/recommend', async ({ request }) => {
      if (opts.recommend404) return HttpResponse.json({ error: 'not found' }, { status: 404 })
      calls.push({ path: 'recommend', body: await request.json() })
      return HttpResponse.json(plan)
    }),
    http.post('/api/vhosts/:vhost/ml/models/:name/predict', async ({ request }) => {
      calls.push({ path: 'predict', body: await request.json() })
      return HttpResponse.json({ items: [{ item: 'p-9', score: 0.8 }] })
    }),
    http.post('/api/vhosts/:vhost/ml/models/:name/feedback', async ({ request }) => {
      calls.push({ path: 'feedback', body: await request.json() })
      return new HttpResponse(null, { status: 204 })
    }),
    http.put('/api/vhosts/:vhost/ml/models/:name/build', async ({ request }) => {
      const body = await request.json()
      calls.push({ path: 'build', body })
      return HttpResponse.json(body)
    }),
    http.put('/api/vhosts/:vhost/ml/datasets/:name/refresh', async ({ params, request }) => {
      calls.push({ path: `refresh ${params.name}`, body: await request.json() })
      return HttpResponse.json({})
    }),
  )
  return calls
}

function wrap(children: ReactNode) {
  signInAs('Editor')
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
    <MantineProvider>
      <QueryClientProvider client={qc}>{children}</QueryClientProvider>
    </MantineProvider>,
  )
}

type User = ReturnType<typeof userEvent.setup>

const field = (scope: HTMLElement, label: RegExp) =>
  within(scope).queryByRole('combobox', { name: label, hidden: true }) ?? within(scope).getByRole('textbox', { name: label, hidden: true })

async function pick(user: User, scope: HTMLElement, label: RegExp, option: string) {
  const input = field(scope, label)
  await user.click(input)
  await waitFor(() => expect(document.getElementById(input.getAttribute('aria-controls') ?? '')).toBeTruthy())
  const list = document.getElementById(input.getAttribute('aria-controls') as string) as HTMLElement
  await user.click(await within(list).findByRole('option', { name: option, hidden: true }))
}

const nextButton = (dialog: HTMLElement) => within(dialog).getByRole('button', { name: /^next$/i })
const next = (user: User, dialog: HTMLElement) => user.click(nextButton(dialog))
const role = (dialog: HTMLElement, name: RegExp) => within(dialog).getByRole('group', { name })

const CARD: Record<Scenario, RegExp> = {
  visits: /customers to visit or call/i,
  products: /products to offer a customer/i,
  reorder: /when will they buy again/i,
  audience: /who will want this product/i,
  similar: /similar products/i,
}

/** A new model named `name` on orders, with Recommend and the scenario card chosen, at "What data you need". */
async function toNeeds(user: User, scenario: Scenario, name = 'plan') {
  const dialog = await screen.findByRole('dialog')
  await user.type(within(dialog).getByRole('textbox', { name: /model name/i }), name)
  await pick(user, dialog, /^dataset/i, 'orders')
  await next(user, dialog)
  await user.click(within(dialog).getByRole('radio', { name: /recommend items to people/i }))
  await user.click(await within(dialog).findByRole('radio', { name: CARD[scenario] }))
  await next(user, dialog)
  await within(dialog).findByRole('group', { name: /orders or invoice lines/i })
  return dialog
}

describe('scenario data roles', () => {
  it('needs transactions everywhere, a product only for product scenarios, and customers with a salesman for visits', () => {
    const req = (s: Scenario) => scenarioRoles(s).filter((r) => r.required).map((r) => r.role)
    expect(req('visits')).toEqual(['transactions', 'customers'])
    for (const s of ['products', 'reorder', 'audience', 'similar'] as const) expect(req(s)).toEqual(['transactions'])
    const tx = (s: Scenario) => scenarioRoles(s)[0].columns.filter((c) => c.required).map((c) => c.key)
    expect(tx('products')).toContain('product')
    expect(tx('audience')).toContain('product')
    expect(tx('similar')).toContain('product')
    expect(tx('reorder')).not.toContain('product')
    expect(tx('visits')).not.toContain('product')
    expect(scenarioRoles('visits').find((r) => r.role === 'customers')!.columns.filter((c) => c.required).map((c) => c.key))
      .toEqual(['id', 'salesman'])
  })

  it('guesses id, date and money columns from their names and types', () => {
    expect(guessColumns('transactions', DATASETS.orders.columns as any))
      .toEqual({ customer: 'cust_id', product: 'sku', date: 'order_date', quantity: 'qty', amount: 'total' })
    expect(guessColumns('customers', DATASETS.customers.columns as any))
      .toEqual({ id: 'cust_id', salesman: 'rep', territory: 'region', lat: 'lat', lon: 'lon', blocked: 'credit_hold' })
    expect(guessColumns('products', DATASETS.items.columns as any))
      .toEqual({ id: 'sku', category: 'cat', price: 'price', margin: 'margin', active: 'active' })
    // A money name on a text column is not money; a column is used once.
    expect(guessColumns('transactions', [col('customer'), col('amount'), col('created_at')] as any))
      .toEqual({ customer: 'customer', date: 'created_at' })
  })

  it('makes a CSV template with the example rows', () => {
    const csv = csvTemplate('transactions').split('\n')
    expect(csv[0]).toBe('customer_id,product_id,order_date,quantity,amount')
    expect(csv.length).toBeGreaterThanOrEqual(4)
    expect(csv[1]).toMatch(/^C-1001,/)
  })
})

describe('rules as sentences', () => {
  const data = {
    transactions: { dataset: 'orders', customer: 'cust_id', product: 'sku', date: 'order_date' },
    customers: { dataset: 'customers', id: 'cust_id', salesman: 'rep', territory: 'region', blocked: 'credit_hold' },
    products: { dataset: 'items', id: 'sku', category: 'cat', margin: 'margin', active: 'active' },
  }

  it('gives a visit plan its defaults as contract rules', () => {
    expect(rulesFromChoices('visits', defaultRuleChoices('visits'), data)).toEqual([
      { kind: 'filter', field: 'customers.salesman', op: 'eq', from_request: 'salesman' },
      { kind: 'exclude', days: 7 },
      { kind: 'filter', field: 'customers.blocked', op: 'neq', value: true },
      { kind: 'capacity', per_day: 8, value: ['mon', 'tue', 'wed', 'thu', 'fri'] },
    ])
  })

  it('writes every product rule that is switched on, and none that the scenario or the mapping does not offer', () => {
    const choices = {
      ...defaultRuleChoices('products'), territory: true, excludeDays: 30, excludeOn: true, preferMargin: true,
      promoteIds: ['p-1', 'p-2'], maxPerCategoryOn: true, maxPerCategory: 2,
    }
    expect(rulesFromChoices('products', choices, data)).toEqual([
      { kind: 'exclude', days: 30 },
      { kind: 'filter', field: 'products.active', op: 'eq', value: true },
      { kind: 'boost', ids: ['p-1', 'p-2'], weight: 0.2 },
      { kind: 'boost', field: 'products.margin', weight: 0.2 },
      { kind: 'diversity', field: 'products.category', max_per: 2 },
    ])
    expect(rulesFromChoices('products', choices, { transactions: data.transactions }))
      .toEqual([{ kind: 'exclude', days: 30 }, { kind: 'boost', ids: ['p-1', 'p-2'], weight: 0.2 }])
  })

  it('leaves the working days to the salesmen table when it has them', () => {
    const rules = rulesFromChoices('visits', defaultRuleChoices('visits'), {
      ...data, salesmen: { dataset: 'reps', id: 'rep', working_days: 'days' },
    })
    expect(rules.find((r) => r.kind === 'capacity')).toEqual({ kind: 'capacity', per_day: 8 })
  })
})

describe('result headlines', () => {
  it('says how many planned customers ordered, against the most overdue list', () => {
    const h = scenarioHeadline(visitsVersion as any)
    expect(h.headline).toBe("Of the customers Hermod would have planned last period, 62% ordered — the 'most overdue first' list got 48%")
    expect(h.verdict).toBe('fair')
  })

  it('says products in hits per 100 customers against the popular items', () => {
    const h = scenarioHeadline(productsVersion as any)
    expect(h.headline).toMatch(/for 34 of 100 customers/i)
    expect(h.headline).toMatch(/most popular products .* 12/i)
    expect(h.verdict).toBe('good')
  })

  it('says how far the reorder date was off, lower being better', () => {
    const h = scenarioHeadline({ ...visitsVersion, scenario: 'reorder', metrics: { mae_days: 6, on_time_share: 0.7 }, baseline: { mae_days: 11 }, baselines_named: [] } as any)
    expect(h.headline).toMatch(/6 days/)
    expect(h.headline).toMatch(/11 days/)
    expect(h.verdict).toBe('good')
  })
})

describe('Training a recommendation scenario in the guided dialog', () => {
  it('plans visits: cards, data, health, rules and weights, train, the headline and a salesman preview', async () => {
    const calls = api()
    wrap(<TrainModal vhost="tenant-a" onClose={() => {}} />)
    const user = userEvent.setup()
    const dialog = await toNeeds(user, 'visits')

    // What data you need: transactions came from step 1, with its columns guessed.
    const tx = role(dialog, /orders or invoice lines/i)
    expect(field(tx, /^customer/i)).toHaveValue('cust_id')
    expect(field(tx, /^date/i)).toHaveValue('order_date')
    expect(within(tx).getByRole('table', { name: /example/i })).toBeInTheDocument()
    expect(within(tx).getByRole('button', { name: /download csv template/i })).toBeInTheDocument()
    // Customers are required for visits, so Next waits for them.
    expect(nextButton(dialog)).toBeDisabled()
    const cust = role(dialog, /^customers/i)
    await pick(user, cust, /customers dataset/i, 'customers')
    await waitFor(() => expect(field(cust, /^salesman/i)).toHaveValue('rep'))
    expect(field(cust, /^credit hold/i)).toHaveValue('credit_hold')
    await waitFor(() => expect(nextButton(dialog)).toBeEnabled())
    await next(user, dialog)

    // Data health: the checks with their fixes, and the stats.
    expect(await within(dialog).findByText(/planning for 3 months needs at least 6/i)).toBeInTheDocument()
    expect(within(dialog).getByText(/1,240 customers/)).toBeInTheDocument()
    const check = calls.find((c) => c.path === 'check' && c.body.data)!.body
    expect(check).toEqual({
      task: 'recommendation', scenario: 'visits',
      data: {
        transactions: { dataset: 'orders', customer: 'cust_id', product: 'sku', date: 'order_date', quantity: 'qty', amount: 'total' },
        customers: { dataset: 'customers', id: 'cust_id', salesman: 'rep', territory: 'region', lat: 'lat', lon: 'lon', blocked: 'credit_hold' },
      },
    })
    expect(check).not.toHaveProperty('rules')
    await next(user, dialog)

    // Rules: sentences, and the weights in plain words with a reset.
    expect(within(dialog).getByRole('checkbox', { name: /only the salesman's own customers/i })).toBeChecked()
    const perDay = within(dialog).getByRole('textbox', { name: /visits per day/i })
    await user.clear(perDay)
    await user.type(perDay, '6')
    expect(within(dialog).getByRole('slider', { name: /overdue orders/i })).toHaveAttribute('aria-valuenow', '35')
    within(dialog).getByRole('slider', { name: /overdue orders/i }).focus()
    await user.keyboard('{ArrowRight}')
    expect(within(dialog).getByRole('slider', { name: /overdue orders/i })).toHaveAttribute('aria-valuenow', '40')
    await user.click(within(dialog).getByRole('button', { name: /reset the weights/i }))
    expect(within(dialog).getByRole('slider', { name: /overdue orders/i })).toHaveAttribute('aria-valuenow', '35')
    await next(user, dialog)

    await user.click(within(dialog).getByRole('button', { name: /^train$/i }))
    await waitFor(() => expect(calls.find((c) => c.path === 'train plan')).toBeTruthy())
    const body = calls.find((c) => c.path === 'train plan')!.body
    expect(body).toMatchObject({
      task: 'recommendation', scenario: 'visits', algorithm: 'auto',
      data: check.data,
      weights: { reorder_due: 0.35, churn_risk: 0.2, value: 0.2, opportunity: 0.1, neglect: 0.15 },
      rules: [
        { kind: 'filter', field: 'customers.salesman', op: 'eq', from_request: 'salesman' },
        { kind: 'exclude', days: 7 },
        { kind: 'filter', field: 'customers.blocked', op: 'neq', value: true },
        { kind: 'capacity', per_day: 6, value: ['mon', 'tue', 'wed', 'thu', 'fri'] },
      ],
    })
    expect(body).not.toHaveProperty('recommend')

    // Result: the headline, the named baselines and a plan preview.
    expect(await within(dialog).findByText(/62% ordered — the 'most overdue first' list got 48%/)).toBeInTheDocument()
    expect(within(dialog).getByText(/^fair$/i)).toBeInTheDocument()
    expect(within(dialog).getByText(/biggest customers first/i)).toBeInTheDocument()
    expect(within(dialog).getByText(/usually orders every 30 days; last order 41 days ago/i)).toBeInTheDocument()

    await user.type(within(dialog).getByRole('textbox', { name: /^salesman/i }), 'rep7')
    await user.click(within(dialog).getByRole('button', { name: /show the plan/i }))
    await waitFor(() => expect(calls.find((c) => c.path === 'recommend')).toBeTruthy())
    const asked = calls.find((c) => c.path === 'recommend')!.body
    expect(asked.for).toEqual({ salesman: 'rep7' })
    expect(asked.period.from).toMatch(/^\d{4}-\d{2}-\d{2}$/)
    expect(asked.period.to).toMatch(/^\d{4}-\d{2}-\d{2}$/)
    const day = await within(dialog).findByRole('list', { name: /2026-10-14/ })
    expect(within(day).getByText(/not visited for 45 days/i)).toBeInTheDocument()
    expect(within(day).getByText(/shop c-9/i)).toBeInTheDocument()
  }, 30_000)

  const cases: Array<{ scenario: Scenario; product: boolean }> = [
    { scenario: 'products', product: true },
    { scenario: 'reorder', product: false },
    { scenario: 'audience', product: true },
    { scenario: 'similar', product: true },
  ]
  it.each(cases)('trains $scenario from the orders alone', async ({ scenario, product }) => {
    const calls = api({ version: productsVersion })
    wrap(<TrainModal vhost="tenant-a" onClose={() => {}} />)
    const user = userEvent.setup()
    const dialog = await toNeeds(user, scenario, 'offers')
    const tx = role(dialog, /orders or invoice lines/i)
    const productField = field(tx, /^product/i)
    expect(productField).toHaveValue('sku')
    if (product) expect(productField).toBeRequired()
    else expect(productField).not.toBeRequired()
    await waitFor(() => expect(nextButton(dialog)).toBeEnabled())
    await next(user, dialog)
    await within(dialog).findByText(/planning for 3 months needs at least 6/i)
    await next(user, dialog)
    expect(within(dialog).queryByRole('slider')).not.toBeInTheDocument()
    await next(user, dialog)
    await user.click(within(dialog).getByRole('button', { name: /^train$/i }))
    await waitFor(() => expect(calls.find((c) => c.path === 'train offers')).toBeTruthy())
    const body = calls.find((c) => c.path === 'train offers')!.body
    expect(body.scenario).toBe(scenario)
    expect(body.data.transactions).toMatchObject({ dataset: 'orders', customer: 'cust_id', product: 'sku' })
    expect(body).not.toHaveProperty('weights')
    expect(await within(dialog).findByText(/for 34 of 100/i)).toBeInTheDocument()
  })

  it('stops at a red health check and says how to fix it', async () => {
    api({
      check: {
        ok: false,
        checks: [{ code: 'few_customers', status: 'red', message: 'Only 120 customers', fix: 'Product recommendations need at least 1,000 customers.' }],
        stats: { customers: 120, products: 40, transactions: 300, months_of_history: 2, tx_per_customer: 2.5 },
      },
    })
    wrap(<TrainModal vhost="tenant-a" onClose={() => {}} />)
    const user = userEvent.setup()
    const dialog = await toNeeds(user, 'products', 'offers')
    await waitFor(() => expect(nextButton(dialog)).toBeEnabled())
    await next(user, dialog)
    expect(await within(dialog).findByText(/need at least 1,000 customers/i)).toBeInTheDocument()
    expect(within(dialog).getByText(/only 120 customers/i)).toBeInTheDocument()
    expect(nextButton(dialog)).toBeDisabled()
  })

  it('opens the data guide from the scenario step', async () => {
    api()
    wrap(<TrainModal vhost="tenant-a" onClose={() => {}} />)
    const user = userEvent.setup()
    const dialog = await screen.findByRole('dialog')
    await user.type(within(dialog).getByRole('textbox', { name: /model name/i }), 'plan')
    await pick(user, dialog, /^dataset/i, 'orders')
    await next(user, dialog)
    await user.click(within(dialog).getByRole('radio', { name: /recommend items to people/i }))
    await user.click(await within(dialog).findByRole('button', { name: /how to prepare your data/i }))
    const guide = await screen.findByRole('dialog', { name: /how to prepare your data/i })
    expect(within(guide).getByText(/at least 1,000 customers/i)).toBeInTheDocument()
    expect(within(guide).getByText(/twice the planning period/i)).toBeInTheDocument()
  })

  it('falls back to the one-table recommender on a server without scenarios', async () => {
    const calls = api({ scenarios: false })
    wrap(<TrainModal vhost="tenant-a" onClose={() => {}} />)
    const user = userEvent.setup()
    const dialog = await screen.findByRole('dialog')
    await user.type(within(dialog).getByRole('textbox', { name: /model name/i }), 'recs')
    await pick(user, dialog, /^dataset/i, 'orders')
    await next(user, dialog)
    await user.click(within(dialog).getByRole('radio', { name: /recommend items to people/i }))
    await waitFor(() => expect(field(dialog, /which column is the person/i)).toBeInTheDocument())
    expect(within(dialog).queryByRole('radio', { name: CARD.visits })).not.toBeInTheDocument()
    expect(calls.filter((c) => c.path === 'check')).toHaveLength(0)
  })
})

const planModel: MLModel = { name: 'plan', backend: 'hermod-ml', url: '', remote_version: '1', serving: false, task: 'recommendation' }

describe('Trying a scenario model', () => {
  it("asks for a salesman's plan and posts whether it was useful", async () => {
    const calls = api()
    wrap(<ModelTestModal vhost="tenant-a" model={planModel} onClose={() => {}} />)
    const user = userEvent.setup()
    const dialog = await screen.findByRole('dialog')
    await user.type(await within(dialog).findByRole('textbox', { name: /^salesman/i }), 'rep7')
    await user.click(within(dialog).getByRole('button', { name: /^recommend$/i }))
    await waitFor(() => expect(calls.find((c) => c.path === 'recommend')).toBeTruthy())
    expect(calls.find((c) => c.path === 'recommend')!.body).toMatchObject({ for: { salesman: 'rep7' }, k: 10 })
    expect(await within(dialog).findByText(/usually orders every 30 days/i)).toBeInTheDocument()

    await user.click(within(dialog).getByRole('button', { name: /c-7 was useful/i }))
    await waitFor(() => expect(calls.find((c) => c.path === 'feedback')).toBeTruthy())
    expect(calls.find((c) => c.path === 'feedback')!.body).toEqual({
      recommendation_id: 'rec_1', events: [{ id: 'c-7', outcome: 'accepted' }],
    })
    await user.click(within(dialog).getByRole('button', { name: /c-9 was not useful/i }))
    await waitFor(() => expect(calls.filter((c) => c.path === 'feedback')).toHaveLength(2))
    expect(calls.filter((c) => c.path === 'feedback')[1].body.events).toEqual([{ id: 'c-9', outcome: 'dismissed' }])
  })

  it('asks the old predict route when the server has no recommend route', async () => {
    const calls = api({ version: productsVersion, recommend404: true })
    wrap(<ModelTestModal vhost="tenant-a" model={{ ...planModel, name: 'offers' }} onClose={() => {}} />)
    const user = userEvent.setup()
    const dialog = await screen.findByRole('dialog')
    await user.type(await within(dialog).findByRole('textbox', { name: /^customer/i }), 'c-1')
    await user.click(within(dialog).getByRole('button', { name: /^recommend$/i }))
    await waitFor(() => expect(calls.find((c) => c.path === 'predict')).toBeTruthy())
    expect(calls.find((c) => c.path === 'predict')!.body).toEqual({ user: 'c-1', k: 10, exclude_seen: true })
    expect(await within(dialog).findByText('p-9')).toBeInTheDocument()
  })
})

describe('Using a scenario model', () => {
  it('copies the sync and async calls and saves a weekly build into a table', async () => {
    const calls = api()
    wrap(<TrainModal vhost="tenant-a" modelName="plan" task="recommendation" onClose={() => {}} />)
    const user = userEvent.setup()
    const dialog = await screen.findByRole('dialog')
    // Training again starts from the last version's scenario and roles.
    await within(dialog).findByRole('group', { name: /orders or invoice lines/i })
    const cust = role(dialog, /^customers/i)
    await pick(user, cust, /customers dataset/i, 'customers')
    await waitFor(() => expect(nextButton(dialog)).toBeEnabled())
    await next(user, dialog)
    await within(dialog).findByText(/planning for 3 months/i)
    await next(user, dialog)
    await next(user, dialog)
    await user.click(within(dialog).getByRole('button', { name: /^train$/i }))

    await user.click(await within(dialog).findByRole('button', { name: /copy api call/i }))
    const copied = await navigator.clipboard.readText()
    expect(copied).toContain('/api/ml/serve/tenant-a/plan/recommend')
    expect(copied).toContain('"for":{"salesman":"<salesman id>"}')
    expect(copied).not.toContain('async')
    await user.click(within(dialog).getByRole('button', { name: /copy async call/i }))
    expect(await navigator.clipboard.readText()).toContain('"respond":"async"')
    expect(within(dialog).getByText(/requires hermod with async jobs/i)).toBeInTheDocument()

    await user.click(within(dialog).getByRole('button', { name: /build recommendations into a table/i }))
    const form = await screen.findByRole('dialog', { name: /build recommendations/i })
    await pick(user, form, /^database/i, 'warehouse')
    await user.clear(within(form).getByRole('textbox', { name: /^table/i }))
    await user.type(within(form).getByRole('textbox', { name: /^table/i }), 'visit_plan')
    await user.click(within(form).getByRole('button', { name: /^save$/i }))
    await waitFor(() => expect(calls.find((c) => c.path === 'build')).toBeTruthy())
    expect(calls.find((c) => c.path === 'build')!.body).toEqual({
      schedule: '0 6 * * 1', for: 'all_salesmen', period_days: 7, k: 20,
      target: { source_id: 'pg1', table: 'visit_plan', mode: 'replace' },
    })
  })
})

describe('Refreshing a dataset on a schedule', () => {
  it('shows the last run and error, and saves the schedule and query', async () => {
    const calls = api()
    wrap(<ConfirmProvider><DatasetsPanel vhost="tenant-a" /></ConfirmProvider>)
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: /refresh orders on a schedule/i }))
    const dialog = await screen.findByRole('dialog', { name: /refresh orders/i })
    expect(await within(dialog).findByText(/connection refused/i)).toBeInTheDocument()
    expect(within(dialog).getByRole('textbox', { name: /^schedule/i })).toHaveValue('0 5 * * *')
    await pick(user, dialog, /^database source/i, 'warehouse')
    await user.type(within(dialog).getByRole('textbox', { name: /^query/i }), 'SELECT * FROM orders')
    await user.click(within(dialog).getByRole('button', { name: /^save$/i }))
    await waitFor(() => expect(calls.find((c) => c.path === 'refresh orders')).toBeTruthy())
    expect(calls.find((c) => c.path === 'refresh orders')!.body).toEqual({
      schedule: '0 5 * * *', source_id: 'pg1', query: 'SELECT * FROM orders', max_rows: 0,
    })
  })
})

describe('Monitoring a scenario model', () => {
  it('shows the acceptance rate of the last 30 days', async () => {
    api()
    server.use(
      http.get('/api/vhosts/:vhost/ml/models/:name/predictions', () => HttpResponse.json({ data: [], total: 0 })),
      http.get('/api/vhosts/:vhost/ml/models/:name/drift', () => HttpResponse.json({ report: null, window: '5m0s', min_rows: 100 })),
      http.get('/api/vhosts/:vhost/ml/models/:name/feedback/summary', ({ request }) => {
        expect(new URL(request.url).searchParams.get('days')).toBe('30')
        return HttpResponse.json({ shown: 200, accepted: 46, dismissed: 20, visited: 30, ordered: 12, acceptance_rate: 0.23 })
      }),
    )
    wrap(<MonitoringModal vhost="tenant-a" model={planModel} onClose={() => {}} />)
    const user = userEvent.setup()
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('tab', { name: /coverage/i }))
    expect(await within(dialog).findByText(/46 of 200 shown \(23%\)/)).toBeInTheDocument()
  })
})

function Harness({ Editor, initial, onConfig }: { Editor: any; initial: Record<string, any>; onConfig: (c: any) => void }) {
  const [config, setConfig] = useState<Record<string, any>>(initial)
  return (
    <Editor nodeId="n1" config={config} fieldPaths={['rep']}
      updateNodeConfig={(_id: string, patch: any) => setConfig((c) => {
        const merged = { ...c, ...patch }
        onConfig(merged)
        return merged
      })} />
  )
}

describe('Workflow nodes for scenarios', () => {
  it('asks a visit model for a salesman and a period in the Predict node', async () => {
    api()
    server.use(http.get('/api/vhosts/:vhost/ml/models', () => HttpResponse.json({ data: [planModel], total: 1 })))
    useWorkflowStore.setState({ vhost: 'tenant-a' } as any)
    let last: any = {}
    wrap(<Harness Editor={MLPredictConfig} initial={{ model: 'plan' }} onConfig={(c) => { last = c }} />)
    const user = userEvent.setup()
    await waitFor(() => expect(last.mode).toBe('recommend'))
    await waitFor(() => expect(field(document.body, /field with the salesman id/i)).toBeInTheDocument())
    await user.type(field(document.body, /field with the salesman id/i), 'rep')
    await user.type(screen.getByRole('textbox', { name: /days to plan/i }), '5')
    expect(last).toMatchObject({ recommendFor: 'salesman', idField: 'rep', periodDays: '5' })
  })

  it('has a Build Recommendations node in the palette with its own editor', async () => {
    const ml = NODE_CATEGORIES.find((c: any) => c.title === 'Machine Learning')
    // The engine runs it as its own node type (RegisterNodeExecutor
    // "ml_recommend_build"), not as a transformation subtype.
    const item = ml?.items.find((i: any) => i.subType === 'ml_recommend_build')
    expect(item?.type).toBe('ml_recommend_build')
    expect(resolveConfigComponent('ml_recommend_build', 'ml_recommend_build')).toBe(MLRecommendBuildConfig)
    expect(canvasNodeTypes).toHaveProperty('ml_recommend_build')
    expect(detailNodeTypes).toHaveProperty('ml_recommend_build')

    api()
    server.use(http.get('/api/vhosts/:vhost/ml/models', () => HttpResponse.json({ data: [planModel], total: 1 })))
    useWorkflowStore.setState({ vhost: 'tenant-a' } as any)
    let last: any = {}
    wrap(<Harness Editor={MLRecommendBuildConfig} initial={{}} onConfig={(c) => { last = c }} />)
    const user = userEvent.setup()
    await pick(user, document.body, /^model/i, 'plan')
    await waitFor(() => expect(last.for).toBe('all_salesmen'))
    await user.type(screen.getByRole('textbox', { name: /^table/i }), 'visit_plan')
    expect(last).toMatchObject({ model: 'plan', for: 'all_salesmen', targetTable: 'visit_plan' })
  })
})

describe('Customers likely to buy in the next N days', () => {
  const due = {
    recommendation_id: 'rec_9',
    results: [{
      for: 'all_customers',
      items: [
        item('c-3', 1, 'Usually orders every 14 days; last order 16 days ago', {
          due_date: '2026-10-11', overdue: true,
          products: [{ id: 'sku-1', due_date: '2026-10-09', overdue: true, fields: { name: 'Coffee 1kg' } }],
        }),
        item('c-8', 2, 'Usually orders every 30 days; last order 20 days ago', {
          due_date: '2026-10-23',
          products: [{ id: 'sku-2', due_date: '2026-10-24', fields: { name: 'Sugar 5kg' } }],
        }),
      ],
    }],
  }
  const reorderModel: MLModel = { ...planModel, name: 'due' }

  function dueApi() {
    const calls = api()
    server.use(http.post('/api/vhosts/:vhost/ml/models/:name/recommend', async ({ request }) => {
      calls.push({ path: 'recommend', body: await request.json() })
      return HttpResponse.json(due)
    }))
    return calls
  }

  it('lists everyone due in the next 2 weeks, overdue ones marked, with the products due', async () => {
    const calls = dueApi()
    wrap(<ModelTestModal vhost="tenant-a" model={reorderModel} scenario="reorder" onClose={() => {}} />)
    const user = userEvent.setup()
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByRole('radio', { name: /everyone/i })).toBeChecked()
    expect(within(dialog).getByRole('radio', { name: /2 weeks/i })).toBeChecked()
    await user.click(within(dialog).getByRole('button', { name: /find customers/i }))
    await waitFor(() => expect(calls.find((c) => c.path === 'recommend')).toBeTruthy())
    expect(calls.find((c) => c.path === 'recommend')!.body).toEqual({ for: { all_customers: true }, within_days: 14, k: 100 })
    const list = await within(dialog).findByRole('list', { name: /customers likely to buy/i })
    expect(within(list).getByText('Shop c-3')).toBeInTheDocument()
    expect(within(list).getByText(/overdue since 2026-10-11/i)).toBeInTheDocument()
    expect(within(list).getByText(/expected 2026-10-23/i)).toBeInTheDocument()
    expect(within(list).getByText(/coffee 1kg/i)).toBeInTheDocument()
    expect(within(list).getByText(/usually orders every 14 days/i)).toBeInTheDocument()
  })

  it("asks a month, a custom number of days, and a salesman's or a product's customers", async () => {
    const calls = dueApi()
    wrap(<ModelTestModal vhost="tenant-a" model={reorderModel} scenario="reorder" onClose={() => {}} />)
    const user = userEvent.setup()
    const dialog = await screen.findByRole('dialog')
    const asked = () => calls.filter((c) => c.path === 'recommend').map((c) => c.body)

    await user.click(within(dialog).getByRole('radio', { name: /1 month/i }))
    await user.click(within(dialog).getByRole('radio', { name: /a salesman's customers/i }))
    await user.type(within(dialog).getByRole('textbox', { name: /^salesman/i }), 'rep7')
    await user.click(within(dialog).getByRole('button', { name: /find customers/i }))
    await waitFor(() => expect(asked()).toHaveLength(1))
    expect(asked()[0]).toEqual({ for: { salesman: 'rep7' }, within_days: 30, k: 100 })

    await user.click(within(dialog).getByRole('radio', { name: /^custom$/i }))
    const days = within(dialog).getByRole('textbox', { name: /^days/i })
    await user.clear(days)
    await user.type(days, '45')
    await user.click(within(dialog).getByRole('radio', { name: /a product's buyers/i }))
    await user.type(within(dialog).getByRole('textbox', { name: /^product/i }), 'sku-1')
    await user.click(within(dialog).getByRole('button', { name: /find customers/i }))
    await waitFor(() => expect(asked()).toHaveLength(2))
    expect(asked()[1]).toEqual({ for: { product: 'sku-1' }, within_days: 45, k: 100 })

    // "Any time" is the call as before: one customer's next order date.
    await user.click(within(dialog).getByRole('radio', { name: /any time/i }))
    await user.click(within(dialog).getByRole('radio', { name: /one customer/i }))
    await user.type(within(dialog).getByRole('textbox', { name: /^customer/i }), 'c-3')
    await user.click(within(dialog).getByRole('button', { name: /find customers/i }))
    await waitFor(() => expect(asked()).toHaveLength(3))
    expect(asked()[2]).toEqual({ for: { customer: 'c-3' }, k: 100 })
  })

  it('builds only the customers due within a window into a table', async () => {
    const reorderVersion = { ...visitsVersion, scenario: 'reorder', metrics: { mae_days: 4, on_time_share: 0.9 },
      baseline: { mae_days: 9 }, baselines_named: [{ name: 'popular', metrics: { mae_days: 9 } }] }
    const calls = api({ version: reorderVersion })
    wrap(<TrainModal vhost="tenant-a" modelName="due" task="recommendation" onClose={() => {}} />)
    const user = userEvent.setup()
    const dialog = await screen.findByRole('dialog')
    await within(dialog).findByRole('group', { name: /orders or invoice lines/i })
    await waitFor(() => expect(nextButton(dialog)).toBeEnabled())
    await next(user, dialog)
    await waitFor(() => expect(nextButton(dialog)).toBeEnabled())
    await next(user, dialog)
    await next(user, dialog)
    await user.click(within(dialog).getByRole('button', { name: /^train$/i }))
    await user.click(await within(dialog).findByRole('button', { name: /build recommendations into a table/i }))
    const form = await screen.findByRole('dialog', { name: /build recommendations/i })
    expect(within(form).getByText(/due_date/)).toBeInTheDocument()
    await user.type(within(form).getByRole('textbox', { name: /only customers due within/i }), '14')
    await pick(user, form, /^database/i, 'warehouse')
    await user.click(within(form).getByRole('button', { name: /^save$/i }))
    await waitFor(() => expect(calls.find((c) => c.path === 'build')).toBeTruthy())
    expect(calls.find((c) => c.path === 'build')!.body).toEqual({
      schedule: '0 6 * * 1', for: 'all_customers', within_days: 14, k: 20,
      target: { source_id: 'pg1', table: 'recommendations', mode: 'replace' },
    })
  })

  it('asks a reorder model for the customers due within N days in the Predict and Build nodes', async () => {
    const reorderVersion = { ...visitsVersion, scenario: 'reorder' }
    api({ version: reorderVersion })
    server.use(http.get('/api/vhosts/:vhost/ml/models', () => HttpResponse.json({ data: [reorderModel], total: 1 })))
    useWorkflowStore.setState({ vhost: 'tenant-a' } as any)
    let last: any = {}
    const { unmount } = wrap(<Harness Editor={MLPredictConfig} initial={{ model: 'due' }} onConfig={(c) => { last = c }} />)
    const user = userEvent.setup()
    await waitFor(() => expect(last.recommendFor).toBe('customer'))
    await pick(user, document.body, /^ask for/i, "A salesman's customers")
    await waitFor(() => expect(last.recommendFor).toBe('salesman'))
    await user.type(screen.getByRole('textbox', { name: /due within/i }), '14')
    expect(last).toMatchObject({ recommendFor: 'salesman', withinDays: '14' })
    unmount()

    let built: any = {}
    wrap(<Harness Editor={MLRecommendBuildConfig} initial={{ model: 'due' }} onConfig={(c) => { built = c }} />)
    await waitFor(() => expect(built.for).toBe('all_customers'))
    await user.type(screen.getByRole('textbox', { name: /only customers due within/i }), '30')
    expect(built).toMatchObject({ for: 'all_customers', withinDays: '30' })
  })
})

describe('Customer category, size and nearby customers', () => {
  it('maps a customer category and size from their names', () => {
    expect(guessColumns('customers', [col('cust_id'), col('tier'), col('employees', 'number')] as any))
      .toEqual({ id: 'cust_id', segment: 'tier', size: 'employees' })
    const customers = scenarioRoles('products').find((r) => r.role === 'customers')!
    expect(customers.columns.map((c) => c.label)).toEqual(expect.arrayContaining(['Customer category', 'Customer size']))
    expect(csvTemplate('customers').split('\n')[0]).toContain('segment,size')
  })

  it('keeps one category and size of a reorder list, and shows them on each customer', async () => {
    const calls = api()
    server.use(http.post('/api/vhosts/:vhost/ml/models/:name/recommend', async ({ request }) => {
      calls.push({ path: 'recommend', body: await request.json() })
      return HttpResponse.json({ recommendation_id: 'rec_c', results: [{ for: 'all_customers', items: [
        item('c-3', 1, 'Usually orders every 14 days', { due_date: '2026-10-20', fields: { name: 'Shop c-3', segment: 'Retail', size: 'large' } }),
      ] }] })
    }))
    wrap(<ModelTestModal vhost="tenant-a" model={{ ...planModel, name: 'due' }} scenario="reorder" onClose={() => {}} />)
    const user = userEvent.setup()
    const dialog = await screen.findByRole('dialog')
    await user.type(within(dialog).getByRole('textbox', { name: /^category/i }), 'Retail')
    await pick(user, dialog, /^size/i, 'large')
    await user.click(within(dialog).getByRole('button', { name: /find customers/i }))
    await waitFor(() => expect(calls.find((c) => c.path === 'recommend')).toBeTruthy())
    expect(calls.find((c) => c.path === 'recommend')!.body)
      .toEqual({ for: { all_customers: true, segment: 'Retail', size: 'large' }, within_days: 14, k: 100 })
    const list = await within(dialog).findByRole('list', { name: /customers likely to buy/i })
    expect(within(list).getByText('Retail')).toBeInTheDocument()
    expect(within(list).getByText('large')).toBeInTheDocument()
  })

  it("says what nearby customers of a customer's category buy", async () => {
    const calls = api({ version: productsVersion })
    server.use(http.post('/api/vhosts/:vhost/ml/models/:name/recommend', async ({ request }) => {
      calls.push({ path: 'recommend', body: await request.json() })
      return HttpResponse.json({ recommendation_id: 'rec_n', results: [{
        for: 'c-1', summary: 'Minimarket customers within 5 km buy: Coffee, Sugar, Tea',
        items: [{ id: 'p-1', score: 0.9, rank: 1, fields: { name: 'Coffee' },
          reasons: [{ code: 'peer_group', text: 'Bought by 7 of 12 Minimarket customers within 5 km' }] }],
      }] })
    }))
    wrap(<ModelTestModal vhost="tenant-a" model={{ ...planModel, name: 'offers' }} onClose={() => {}} />)
    const user = userEvent.setup()
    const dialog = await screen.findByRole('dialog')
    await user.type(await within(dialog).findByRole('textbox', { name: /^customer$/i }), 'c-1')
    await user.click(within(dialog).getByRole('button', { name: /^recommend$/i }))
    expect(await within(dialog).findByText('Minimarket customers within 5 km buy: Coffee, Sugar, Tea')).toBeInTheDocument()
    expect(within(dialog).getByText(/bought by 7 of 12 minimarket customers within 5 km/i)).toBeInTheDocument()
  })

  it('asks how far nearby customers may be when the customers have a category and coordinates', async () => {
    const customers = { dataset: 'customers', id: 'cust_id', segment: 'tier', lat: 'lat', lon: 'lon' }
    function Step({ data }: { data: RecommendData }) {
      const [km, setKm] = useState(5)
      return (
        <>
          <RulesStep scenario="products" data={data} choices={defaultRuleChoices('products')} onChoices={() => {}}
            weights={{} as any} onWeights={() => {}} nearKm={km} onNearKm={setKm} />
          <output data-testid="km">{km}</output>
        </>
      )
    }
    const view = wrap(<Step data={{ customers }} />)
    const user = userEvent.setup()
    const radius = screen.getByRole('textbox', { name: /nearby customers within/i })
    expect(radius).toHaveValue('5')
    await user.clear(radius)
    await user.type(radius, '12')
    expect(screen.getByTestId('km')).toHaveTextContent('12')
    view.unmount()
    wrap(<Step data={{ customers: { dataset: 'customers', id: 'cust_id', segment: 'tier' } }} />)
    expect(screen.queryByRole('textbox', { name: /nearby customers within/i })).not.toBeInTheDocument()
  })
})

describe('Customer category and size in the Predict node', () => {
  it('keeps a list of customers to one category and size', async () => {
    api({ version: { ...visitsVersion, scenario: 'reorder' } })
    server.use(http.get('/api/vhosts/:vhost/ml/models', () => HttpResponse.json({ data: [{ ...planModel, name: 'due' }], total: 1 })))
    useWorkflowStore.setState({ vhost: 'tenant-a' } as any)
    let last: any = {}
    wrap(<Harness Editor={MLPredictConfig} initial={{ model: 'due' }} onConfig={(c) => { last = c }} />)
    const user = userEvent.setup()
    await waitFor(() => expect(last.recommendFor).toBe('customer'))
    await user.type(screen.getByRole('textbox', { name: /^customer category/i }), 'Retail')
    await user.type(screen.getByRole('textbox', { name: /^customer size/i }), 'large')
    expect(last).toMatchObject({ segment: 'Retail', size: 'large' })
  })
})
