import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { useEffect } from 'react'
import { describe, expect, it } from 'vitest'
import { server, signInAs } from '../test/setupTests'
import { VHostProvider, useVHost } from '@/context/VHostContext'
import { ConfirmProvider } from '@/components/common/ConfirmProvider'
import { AIBudgetPage } from '@/pages/ai/AIBudgetPage'
import type { AIBudget, AIBudgetReport } from '@/lib/aiBudget'

/**
 * AI budget: where a vhost's AI spending is limited. The page shows this
 * month's usage against the limits, warns from 80%, saves the budget and the
 * per-workflow caps, and switches the vhost's AI calls off and on.
 */

function report(budget: Partial<AIBudget>, tokens = 0, costMicros = 0, workflows: AIBudgetReport['workflows'] = []): AIBudgetReport {
  return {
    vhost: 'tenant-a',
    period: '2026-10',
    budget: { disabled: false, ...budget },
    usage: { vhost: 'tenant-a', period: '2026-10', calls: 3, input_tokens: tokens, output_tokens: 0, cost_micros: costMicros },
    workflows,
  }
}

function budgetApi(initial: AIBudgetReport) {
  let state = structuredClone(initial)
  const writes: Array<{ path: string; body: any }> = []
  server.use(
    http.get('/api/vhosts/:vhost/ai/budget', () => HttpResponse.json(state)),
    http.put('/api/vhosts/:vhost/ai/budget', async ({ params, request }) => {
      const body = (await request.json()) as any
      writes.push({ path: `${params.vhost}/budget`, body })
      state = { ...state, budget: body }
      return HttpResponse.json(state)
    }),
    http.put('/api/vhosts/:vhost/ai/kill-switch', async ({ params, request }) => {
      const body = (await request.json()) as any
      writes.push({ path: `${params.vhost}/kill-switch`, body })
      state = { ...state, budget: { ...state.budget, disabled: body.disabled } }
      return HttpResponse.json(state)
    }),
  )
  return writes
}

function SelectVHost({ vhost }: { vhost: string }) {
  const { setSelectedVHost, setAvailableVHosts } = useVHost()
  useEffect(() => {
    setSelectedVHost(vhost)
    setAvailableVHosts([vhost])
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])
  return null
}

function renderPage(role = 'Editor') {
  signInAs(role)
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  render(
    <MantineProvider>
      <QueryClientProvider client={qc}>
        <VHostProvider>
          <ConfirmProvider>
            <SelectVHost vhost="tenant-a" />
            <AIBudgetPage />
          </ConfirmProvider>
        </VHostProvider>
      </QueryClientProvider>
    </MantineProvider>,
  )
}

describe('AI budget page', () => {
  it('shows usage against the budget and warns from 80%', async () => {
    budgetApi(report({ monthly_tokens: 1000, monthly_cost: 10, currency: 'USD', prices: [{ model: '*', input_per_million: 1, output_per_million: 1 }] },
      850, 2_000_000, [{ vhost: 'tenant-a', period: '2026-10', workflow_id: 'wf-1', calls: 1, input_tokens: 120, output_tokens: 0, cost_micros: 0 }]))
    renderPage()

    expect(await screen.findByText('850 of 1,000 tokens')).toBeInTheDocument()
    expect(screen.getByText('85%')).toBeInTheDocument()
    expect(screen.getByText('2.00 of 10.00 USD')).toBeInTheDocument()
    expect(screen.getByRole('alert', { name: /80%/i })).toHaveTextContent(/refused/i)
    expect(screen.getByText('wf-1')).toBeInTheDocument()
  })

  it('saves the budget with a workflow cap', async () => {
    const writes = budgetApi(report({}))
    renderPage()
    const user = userEvent.setup()

    const tokens = await screen.findByRole('textbox', { name: /monthly token budget/i })
    await user.type(tokens, '50000')
    await user.click(screen.getByRole('button', { name: /add workflow cap/i }))
    await user.type(screen.getByRole('textbox', { name: /workflow id 1/i }), 'wf-7')
    await user.type(screen.getByRole('textbox', { name: /token cap 1/i }), '900')
    await user.click(screen.getByRole('button', { name: /save budget/i }))

    await waitFor(() => expect(writes.find((w) => w.path === 'tenant-a/budget')).toBeTruthy())
    const body = writes.find((w) => w.path === 'tenant-a/budget')!.body
    expect(body).toMatchObject({ disabled: false, monthly_tokens: 50000, workflows: [{ workflow_id: 'wf-7', monthly_tokens: 900 }] })
  })

  it('switches AI calls off after confirming, and back on', async () => {
    const writes = budgetApi(report({ monthly_tokens: 10 }))
    renderPage()
    const user = userEvent.setup()

    await user.click(await screen.findByRole('switch', { name: /switch ai calls off/i }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: /switch ai off/i }))
    await waitFor(() => expect(writes).toContainEqual({ path: 'tenant-a/kill-switch', body: { disabled: true } }))
    expect(await screen.findByText(/AI calls are switched off/i)).toBeInTheDocument()

    await user.click(screen.getByRole('switch', { name: /switch ai calls off/i }))
    await waitFor(() => expect(writes).toContainEqual({ path: 'tenant-a/kill-switch', body: { disabled: false } }))
  })

  it('lets a Viewer read the budget but not change it', async () => {
    budgetApi(report({ monthly_tokens: 1000 }, 10))
    renderPage('Viewer')

    expect(await screen.findByText('10 of 1,000 tokens')).toBeInTheDocument()
    expect(screen.getByRole('switch', { name: /switch ai calls off/i })).toBeDisabled()
    expect(screen.queryByRole('button', { name: /save budget/i })).not.toBeInTheDocument()
  })
})
