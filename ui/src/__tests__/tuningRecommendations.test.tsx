import { render, screen, within } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
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
  return render(
    <MantineProvider>
      <Recommendations report={report()} isLoading={false} error={null} {...props} />
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
})
