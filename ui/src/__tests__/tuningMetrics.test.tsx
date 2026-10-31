import { render, screen, within } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { TuningMetrics, type EngineStatus } from '@/pages/workflows/tuning/TuningMetrics'

const MS = 1_000_000 // Go durations arrive as nanoseconds.

function status(overrides: Partial<EngineStatus> = {}): EngineStatus {
  return {
    workflow_id: 'wf-1',
    engine_status: 'running',
    processed_count: 1200,
    dead_letter_count: 3,
    throughput: 12.5,
    error_rate: 0.25,
    backpressure: 0.75,
    avg_latency: 3 * MS,
    sink_buffer_fill: { 'snk-conn': 0.75, 'idle-conn': 0 },
    sink_cb_statuses: { 'snk-conn': 'open', 'idle-conn': 'closed' },
    node_metrics: { src: 1200, map: 1200, slow: 1190 },
    node_error_metrics: { slow: 10 },
    node_latencies: { map: 2 * MS, slow: 450 * MS },
    ...overrides,
  }
}

function renderMetrics(props: Partial<Parameters<typeof TuningMetrics>[0]> = {}) {
  return render(
    <MantineProvider>
      <TuningMetrics
        active
        status={status()}
        nodeNames={new Map([['map', 'Map fields'], ['slow', 'Enrich from API'], ['src', 'Orders DB']])}
        connectionNames={{ 'snk-conn': 'Warehouse', 'idle-conn': 'Audit log' }}
        {...props}
      />
    </MantineProvider>,
  )
}

describe('TuningMetrics', () => {
  it('says the flow is off instead of showing zeros', () => {
    renderMetrics({ active: false, status: null })
    expect(screen.getByText(/turn the flow on to see live metrics/i)).toBeInTheDocument()
    expect(screen.queryByText('0.0 msg/s')).not.toBeInTheDocument()
  })

  it('waits for the first reading of a flow that is on', () => {
    renderMetrics({ status: null })
    expect(screen.getByText(/waiting for the first reading/i)).toBeInTheDocument()
  })

  it('shows the live readings the engine reports', () => {
    renderMetrics()
    const tile = (label: string) => screen.getByText(label).closest('[data-metric]') as HTMLElement
    expect(tile('Throughput')).toHaveTextContent('12.5 msg/s')
    expect(within(tile('Error rate')).getByText('25.00%')).toBeInTheDocument()
    expect(within(tile('Backpressure')).getByText('75%')).toBeInTheDocument()
    expect(within(tile('Avg latency')).getByText('3 ms')).toBeInTheDocument()
    expect(within(tile('Processed')).getByText('1,200')).toBeInTheDocument()
    expect(within(tile('Dead-lettered')).getByText('3')).toBeInTheDocument()
  })

  it('lists each sink by connection name with its buffer and breaker', () => {
    renderMetrics()
    const row = screen.getByText('Warehouse').closest('tr') as HTMLElement
    expect(within(row).getByText('75%')).toBeInTheDocument()
    expect(within(row).getByText(/open/i)).toBeInTheDocument()
    expect(screen.getByText('Audit log')).toBeInTheDocument()
  })

  it('lists the steps slowest first, with their runs and failures', () => {
    renderMetrics()
    const rows = within(screen.getByRole('table', { name: /steps/i })).getAllByRole('row').slice(1)
    expect(within(rows[0]).getByText('Enrich from API')).toBeInTheDocument()
    expect(within(rows[0]).getByText('450 ms')).toBeInTheDocument()
    expect(within(rows[0]).getByText('10')).toBeInTheDocument()
    expect(within(rows[1]).getByText('Map fields')).toBeInTheDocument()
  })

  it('names a high error rate and backpressure in the insights', () => {
    renderMetrics({ status: status({ backpressure: 0.9 }) })
    expect(screen.getByText(/sink buffers are filling up/i)).toBeInTheDocument()
    expect(screen.getByText(/messages are failing/i)).toBeInTheDocument()
  })

  it('says all is well when messages flow cleanly', () => {
    renderMetrics({ status: status({ error_rate: 0, backpressure: 0.1, sink_buffer_fill: {} }) })
    expect(screen.getByText(/running within normal limits/i)).toBeInTheDocument()
  })
})
