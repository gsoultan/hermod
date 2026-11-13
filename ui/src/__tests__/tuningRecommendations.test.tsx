import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { Notifications } from '@mantine/notifications'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { server } from '../test/setupTests'
import { mutationCache } from '@/lib/queryClient'
import { Recommendations } from '@/pages/workflows/tuning/Recommendations'
import type { InsightsReport } from '@/lib/insights'

function report(overrides: Partial<InsightsReport> = {}): InsightsReport {
  return {
    workflow_id: 'wf-1',
    state: 'ready',
    window_start: '2026-11-13T09:00:00Z',
    window_end: '2026-11-13T09:10:00Z',
    samples: 21,
    processed: 60000,
    throughput: 100,
    bottleneck_node_id: 'lookup',
    findings: [
      {
        rule: 'trace_sampling', severity: 'low', title: 'Runs are not being recorded',
        detail: 'Trace sampling is off.', recommendation: 'Set trace sampling to 1% to 5%.',
      },
      {
        rule: 'bottleneck', severity: 'high', node_id: 'lookup', title: 'Find customer is the slowest step',
        detail: 'Find customer takes 97% of step time.', recommendation: 'Keep the lookup cache on.',
        evidence: [{ label: 'Share of step time', value: '97%' }, { label: 'Average per message', value: '40 ms' }],
      },
    ],
    ...overrides,
  }
}

function renderIt(props: Partial<Parameters<typeof Recommendations>[0]> = {}) {
  // The app's own mutation cache, so its fallback "Action failed" toast is in play.
  return render(
    <MantineProvider>
      <Notifications />
      <QueryClientProvider client={new QueryClient({ mutationCache, defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })}>
        <Recommendations workflowId="wf-1" canApply={false} report={report()} isLoading={false} error={null} {...props} />
      </QueryClientProvider>
    </MantineProvider>,
  )
}

describe('Recommendations', () => {
  it('lists every finding with what to do and the numbers behind it', () => {
    renderIt()
    const card = screen.getByText('Find customer is the slowest step').closest('[data-finding]') as HTMLElement
    expect(within(card).getByText('Find customer takes 97% of step time.')).toBeInTheDocument()
    expect(within(card).getByText('Keep the lookup cache on.')).toBeInTheDocument()
    expect(within(card).getByText('97%')).toBeInTheDocument()
    expect(within(card).getByText('40 ms')).toBeInTheDocument()
    expect(within(card).getByText(/high/i)).toBeInTheDocument()
  })

  it('shows findings in the order the server ranked them', () => {
    renderIt({ report: report({ findings: [...report().findings].reverse() }) })
    const titles = screen.getAllByRole('heading', { level: 5 }).map((h) => h.textContent)
    expect(titles).toEqual(['Find customer is the slowest step', 'Runs are not being recorded'])
  })

  it('says what window the advice is drawn from', () => {
    renderIt()
    expect(screen.getByText(/last 10 min/i)).toBeInTheDocument()
    expect(screen.getByText(/60,000 messages/)).toBeInTheDocument()
  })

  it('says it is still collecting instead of claiming all is well', () => {
    renderIt({ report: report({ state: 'collecting', findings: [] }) })
    expect(screen.getByText(/collecting readings/i)).toBeInTheDocument()
    expect(screen.queryByText(/nothing to improve/i)).not.toBeInTheDocument()
  })

  it('says when a ready flow has nothing to improve', () => {
    renderIt({ report: report({ findings: [] }) })
    expect(screen.getByText(/nothing to improve/i)).toBeInTheDocument()
  })

  it('says the flow is off', () => {
    renderIt({ report: report({ state: 'not_running', findings: [] }) })
    expect(screen.getByText(/turn the flow on/i)).toBeInTheDocument()
  })

  it('shows the server error when the report cannot load', () => {
    renderIt({ report: undefined, error: new Error('Forbidden') })
    expect(screen.getByText('Forbidden')).toBeInTheDocument()
  })

  const fixable = () => report({
    findings: [
      {
        rule: 'lookup_cache_off', severity: 'medium', node_id: 'lookup', title: 'Find customer has its cache turned off',
        detail: 'Every message makes a round trip.', recommendation: 'Set a ttl.', fix: 'enable_lookup_cache',
      },
      {
        rule: 'bottleneck', severity: 'high', node_id: 'lookup', title: 'Find customer is the slowest step',
        detail: 'Find customer takes 97% of step time.', recommendation: 'Set a ttl.',
      },
    ],
  })

  it('lets an editor turn a recommendation Hermod can apply into a fix to approve', async () => {
    let sent: unknown = null
    server.use(http.post('/api/workflows/wf-1/proposals', async ({ request }) => {
      sent = await request.json()
      return HttpResponse.json({ id: 'p-9', status: 'pending', title: 'Turn on the cache of Find customer' }, { status: 201 })
    }))
    renderIt({ canApply: true, report: fixable() })

    // Only the finding with a fix offers it.
    const buttons = screen.getAllByRole('button', { name: /propose this fix/i })
    expect(buttons).toHaveLength(1)
    await userEvent.click(buttons[0])

    await waitFor(() => expect(sent).toEqual({ rule: 'lookup_cache_off', node_id: 'lookup' }))
    expect(await screen.findByText(/waiting for approval under suggested fixes/i)).toBeInTheDocument()
  })

  it('does not offer a fix to someone who cannot edit the flow', () => {
    renderIt({ canApply: false, report: fixable() })
    expect(screen.queryByRole('button', { name: /propose this fix/i })).not.toBeInTheDocument()
  })

  it('says why a fix could not be proposed', async () => {
    server.use(http.post('/api/workflows/wf-1/proposals', () =>
      HttpResponse.json({ error: 'The flow no longer has this problem' }, { status: 409 })))
    renderIt({ canApply: true, report: fixable() })
    await userEvent.click(screen.getByRole('button', { name: /propose this fix/i }))
    expect(await screen.findByText(/no longer has this problem/i)).toBeInTheDocument()
    // Said once, next to the button: not again in a toast.
    expect(screen.getAllByText(/no longer has this problem/i)).toHaveLength(1)
    expect(screen.queryByText('Action failed')).not.toBeInTheDocument()
  })

  it('asks for a plain-language explanation and shows who wrote it', async () => {
    let asked = 0
    server.use(http.post('/api/workflows/wf-1/insights/explain', () => {
      asked += 1
      return HttpResponse.json({ text: 'Cache the lookup first.\n\nThen move the filter.', provider: 'openai', model: 'gpt-x' })
    }))
    renderIt({ canApply: true, report: report({ can_explain: true }) })

    await userEvent.click(screen.getByRole('button', { name: /explain in plain words/i }))

    expect(await screen.findByText(/cache the lookup first/i)).toBeInTheDocument()
    expect(screen.getByText(/written by gpt-x/i)).toBeInTheDocument()
    expect(screen.getByText(/cannot change the flow/i)).toBeInTheDocument()
    expect(asked).toBe(1)
  })

  it('offers no explanation when the server has no model, nothing to explain, or a reader', () => {
    renderIt({ canApply: true, report: report({ can_explain: false }) })
    expect(screen.queryByRole('button', { name: /explain in plain words/i })).not.toBeInTheDocument()
  })

  it('offers no explanation with no findings', () => {
    renderIt({ canApply: true, report: report({ can_explain: true, findings: [] }) })
    expect(screen.queryByRole('button', { name: /explain in plain words/i })).not.toBeInTheDocument()
  })

  it('offers no explanation to someone who cannot edit the flow', () => {
    renderIt({ canApply: false, report: report({ can_explain: true }) })
    expect(screen.queryByRole('button', { name: /explain in plain words/i })).not.toBeInTheDocument()
  })

  it('says why an explanation could not be written', async () => {
    server.use(http.post('/api/workflows/wf-1/insights/explain', () =>
      HttpResponse.json({ error: 'AI budget: monthly token limit of vhost default reached' }, { status: 429 })))
    renderIt({ canApply: true, report: report({ can_explain: true }) })
    await userEvent.click(screen.getByRole('button', { name: /explain in plain words/i }))
    expect(await screen.findByText(/monthly token limit/i)).toBeInTheDocument()
    expect(screen.getAllByText(/monthly token limit/i)).toHaveLength(1)
    expect(screen.queryByText('Action failed')).not.toBeInTheDocument()
  })
})
