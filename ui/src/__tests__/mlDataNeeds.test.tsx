import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { describe, expect, it } from 'vitest'
import { server, signInAs } from '../test/setupTests'
import { TrainModal } from '@/pages/ml/TrainModal'
import { DatasetsPanel } from '@/pages/ml/DatasetsPanel'
import { ConfirmProvider } from '@/components/common/ConfirmProvider'
import { dataGuide } from '@/lib/mlDataGuide'

/**
 * "What data do I need?" in the console: a link under every choice card that
 * opens the guide of that kind, a checklist of the chosen dataset that stops
 * Next and Train only on red and says why, and help in the dataset forms.
 */

const customers = {
  name: 'customers', rows: 1200, updated_at: '2026-10-10T00:00:00Z',
  columns: [{ name: 'age', type: 'number' }, { name: 'plan', type: 'string' }, { name: 'churned', type: 'string' }],
}
const orders = {
  name: 'orders', rows: 5000, updated_at: '2026-10-10T00:00:00Z',
  columns: [{ name: 'cust_id', type: 'string' }, { name: 'sku', type: 'string' }, { name: 'order_date', type: 'string' }],
}

const goodCheck = {
  task: 'classification', target_kind: 'yes_no', classes: 2, rows: 1200, usable_rows: 1180, columns: [], warnings: [], ok: true,
}

function api({ trainCheck = goodCheck as object, healthCheck = {} as object } = {}) {
  server.use(
    http.get('/api/ml/worker', () => HttpResponse.json({ configured: true, ready: true })),
    http.get('/api/vhosts/:vhost/ml/models', () => HttpResponse.json({ data: [], total: 0 })),
    http.get('/api/vhosts/:vhost/ml/datasets', () => HttpResponse.json({ data: [customers, orders], total: 2 })),
    http.get('/api/vhosts/:vhost/ml/datasets/:name', ({ params }) => HttpResponse.json(params.name === 'orders' ? orders : customers)),
    http.post('/api/vhosts/:vhost/ml/train-check', () => HttpResponse.json(trainCheck)),
    http.post('/api/vhosts/:vhost/ml/recommend-check', async ({ request }) => {
      const body = await request.json() as { data?: unknown }
      if (!body?.data) return HttpResponse.json({ error: 'data is required' }, { status: 400 })
      return HttpResponse.json(healthCheck)
    }),
    http.get('/api/sources', () => HttpResponse.json({ data: [{ id: 'pg1', name: 'warehouse', type: 'postgres' }] })),
  )
}

function wrap(children: ReactNode) {
  signInAs('Editor')
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
    <MantineProvider>
      <QueryClientProvider client={qc}><ConfirmProvider>{children}</ConfirmProvider></QueryClientProvider>
    </MantineProvider>,
  )
}

type User = ReturnType<typeof userEvent.setup>

async function pick(user: User, scope: HTMLElement, label: RegExp, option: string) {
  const input = within(scope).getByRole('combobox', { name: label, hidden: true }) ?? within(scope).getByRole('textbox', { name: label })
  await user.click(input)
  await waitFor(() => expect(document.getElementById(input.getAttribute('aria-controls') ?? '')).toBeTruthy())
  const list = document.getElementById(input.getAttribute('aria-controls') as string) as HTMLElement
  await user.click(await within(list).findByRole('option', { name: option, hidden: true }))
}

const nextButton = (dialog: HTMLElement) => within(dialog).getByRole('button', { name: /^next$/i })

async function toWhatToDo(user: User, dataset = 'customers') {
  const dialog = await screen.findByRole('dialog')
  await user.type(within(dialog).getByRole('textbox', { name: /model name/i }), 'm1')
  await pick(user, dialog, /^dataset/i, dataset)
  await user.click(nextButton(dialog))
  return dialog
}

/** The text an element's aria-describedby points at. */
const describedBy = (el: HTMLElement) =>
  (el.getAttribute('aria-describedby') ?? '').split(' ').map((id) => document.getElementById(id)?.textContent ?? '').join(' ')

describe('What data do I need?', () => {
  it('opens from the predict card with the category guide, and switches to numbers', async () => {
    api()
    const user = userEvent.setup()
    wrap(<TrainModal vhost="default" onClose={() => {}} />)
    const dialog = await toWhatToDo(user)
    await user.click(within(dialog).getByRole('button', { name: 'What data do I need for Predict a category?' }))
    const drawer = await screen.findByRole('dialog', { name: 'What data do I need?' })
    expect(within(drawer).getByRole('heading', { name: 'Predict a category' })).toBeInTheDocument()
    const minimums = within(drawer).getByRole('table', { name: 'How much data' })
    expect(within(minimums).getByRole('row', { name: /rows with an answer at least 10 100 or more/i })).toBeInTheDocument()
    expect(within(drawer).getByRole('button', { name: 'Download classification-template.csv' })).toBeInTheDocument()
    await pick(user, drawer, /^for$/i, 'Predict a number')
    expect(within(drawer).getByRole('heading', { name: 'Predict a number' })).toBeInTheDocument()
  })

  it('opens from the reorder card with what due date, overdue and the next N days mean', async () => {
    api()
    const user = userEvent.setup()
    wrap(<TrainModal vhost="default" onClose={() => {}} />)
    const dialog = await toWhatToDo(user, 'orders')
    await user.click(within(dialog).getByRole('radio', { name: /recommend items to people/i }))
    await user.click(await within(dialog).findByRole('button', { name: 'What data do I need for When will they buy again?' }))
    const drawer = await screen.findByRole('dialog', { name: 'What data do I need?' })
    const terms = within(drawer).getByRole('list', { name: 'What the answers mean' })
    for (const t of ['Due date.', 'Overdue.', 'Next N days.', 'Products due.']) expect(within(terms).getByText(t)).toBeInTheDocument()
    expect(within(drawer).getByText(dataGuide('reorder').query)).toBeInTheDocument()
  })
})

describe('the checklist in the train flow', () => {
  it('a good dataset is all green and Next is free', async () => {
    api()
    const user = userEvent.setup()
    wrap(<TrainModal vhost="default" onClose={() => {}} />)
    const dialog = await toWhatToDo(user)
    await pick(user, dialog, /column to predict/i, 'churned')
    const list = await within(dialog).findByRole('list', { name: /your data against/i })
    await waitFor(() => expect(within(list).getAllByRole('listitem').map((li) => li.getAttribute('data-status'))).toEqual(['green', 'green', 'green']))
    expect(nextButton(dialog)).toBeEnabled()
  })

  it('too few rows is red: Next is disabled and says why', async () => {
    api({ trainCheck: { ...goodCheck, rows: 6, usable_rows: 6, ok: false, warnings: [{ code: 'few_rows', message: 'Only 6 rows', column: 'churned' }] } })
    const user = userEvent.setup()
    wrap(<TrainModal vhost="default" onClose={() => {}} />)
    const dialog = await toWhatToDo(user)
    await pick(user, dialog, /column to predict/i, 'churned')
    const list = await within(dialog).findByRole('list', { name: /your data against/i })
    await waitFor(() => expect(list.querySelector('[data-status="red"]')).toBeTruthy())
    expect(nextButton(dialog)).toBeDisabled()
    expect(describedBy(nextButton(dialog))).toMatch(/needs at least 10 rows with a value in churned/)
  })

  it('a visit plan without a customers table is red at "What data you need"', async () => {
    api()
    const user = userEvent.setup()
    wrap(<TrainModal vhost="default" onClose={() => {}} />)
    const dialog = await toWhatToDo(user, 'orders')
    await user.click(within(dialog).getByRole('radio', { name: /recommend items to people/i }))
    await user.click(await within(dialog).findByRole('radio', { name: /customers to visit or call/i }))
    await user.click(nextButton(dialog))
    await within(dialog).findByRole('group', { name: /orders or invoice lines/i })
    await waitFor(() => expect(describedBy(nextButton(dialog))).toMatch(/Choose a dataset for Customers, and its Customer id and Salesman columns/))
    expect(nextButton(dialog)).toBeDisabled()
  })

  it('the health step measures the data against the minimums', async () => {
    api({
      healthCheck: {
        ok: true, checks: [{ code: 'few_customers', status: 'amber', message: 'You have 40 customers', fix: 'Add more.' }],
        stats: { customers: 40, products: 12, transactions: 900, months_of_history: 8, tx_per_customer: 4 },
      },
    })
    const user = userEvent.setup()
    wrap(<TrainModal vhost="default" onClose={() => {}} />)
    const dialog = await toWhatToDo(user, 'orders')
    await user.click(within(dialog).getByRole('radio', { name: /recommend items to people/i }))
    await user.click(await within(dialog).findByRole('radio', { name: /when will they buy again/i }))
    await user.click(nextButton(dialog))
    await within(dialog).findByRole('group', { name: /orders or invoice lines/i })
    await waitFor(() => expect(nextButton(dialog)).toBeEnabled())
    await user.click(nextButton(dialog))
    const list = await within(dialog).findByRole('list', { name: /your data against/i })
    expect(within(list).getByText('You have 40. Required: at least 2. Recommended: 500 or more.')).toBeInTheDocument()
    expect(nextButton(dialog)).toBeEnabled()
  })
})

describe('help in the dataset forms', () => {
  it('From a database: what the query should return, an example to use, the limits', async () => {
    api()
    const user = userEvent.setup()
    wrap(<DatasetsPanel vhost="default" />)
    await user.click(await screen.findByRole('button', { name: /from a database/i }))
    const dialog = await screen.findByRole('dialog', { name: /read a dataset from a database/i })
    await pick(user, dialog, /what will you train with it/i, 'When will they buy again')
    expect(within(dialog).getByText(/should return customer_id, order_date, and if you have them product_id, quantity, amount/)).toBeInTheDocument()
    await user.click(within(dialog).getByRole('button', { name: 'Use the big-table version' }))
    expect(within(dialog).getByRole('textbox', { name: /^query/i })).toHaveValue(dataGuide('reorder').bigQuery)
    await user.click(within(dialog).getByRole('button', { name: /limits, and tips for big tables/i }))
    expect(within(dialog).getByText(/reads at most 1,000,000 rows/)).toBeInTheDocument()
    expect(within(dialog).getByText(/HERMOD_ML_BUILTIN_MAX_ROWS/)).toBeInTheDocument()
  })

  it('Upload a file: the columns and the template for the kind', async () => {
    api()
    const user = userEvent.setup()
    wrap(<DatasetsPanel vhost="default" />)
    await user.click(await screen.findByRole('button', { name: /upload a file/i }))
    const dialog = await screen.findByRole('dialog', { name: /upload a dataset/i })
    await pick(user, dialog, /what will you train with it/i, 'Customers to visit or call')
    expect(within(dialog).getByRole('button', { name: 'Download visits-customers-template.csv' })).toBeInTheDocument()
  })
})
