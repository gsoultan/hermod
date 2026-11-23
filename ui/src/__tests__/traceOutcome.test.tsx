import { render, screen } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { traceOutcome, type SinkNode } from '@/pages/workflows/traces/traceOutcome'
import { TraceOutcomeBanner, TraceListStatus } from '@/pages/workflows/traces/TraceOutcome'

// "The workflow is running, but the traces say nothing about success."
// A trace was a column of identical blue checks: nothing said whether the
// message reached a sink, and the list showed only an id and a time.

// Five sink nodes that all write to one stdout connection, as in the report.
const sinks: SinkNode[] = [
  { id: 'so-sink', refId: 'stdout-1', name: 'LOG - Print Stdout (Sales Order)' },
  { id: 'do-sink', refId: 'stdout-1', name: 'LOG - Print Stdout (Delivery Order)' },
  { id: 'inv-sink', refId: 'stdout-1', name: 'LOG - Print Stdout (Invoice)' },
]

const s = (node_id: string, error?: string) => ({ node_id, error })

describe('traceOutcome', () => {
  it('names each sink node the message was written through', () => {
    const out = traceOutcome(
      [s('workflow_start'), s('fi'), s('so-sink'), s('do-sink'), s('router'), s('stdout-1'), s('stdout-1')],
      sinks,
    )
    expect(out.kind).toBe('delivered')
    expect(out.deliveredTo).toEqual(['LOG - Print Stdout (Sales Order)', 'LOG - Print Stdout (Delivery Order)'])
  })

  it('is a failure when any step failed, naming the first', () => {
    const out = traceOutcome([s('workflow_start'), s('so-sink'), s('stdout-1', 'connection refused')], sinks)
    expect(out.kind).toBe('failed')
    expect(out.failedAt?.node_id).toBe('stdout-1')
    expect(out.failedAt?.error).toBe('connection refused')
  })

  // A filter that drops a message, or a branch with no edge for its outcome,
  // ends the journey without a sink. That is not a delivery.
  it('says so when the message reached no sink', () => {
    expect(traceOutcome([s('workflow_start'), s('fi'), s('router')], sinks).kind).toBe('no_sink')
  })

  it('reaching a sink node is not enough: the write has to be on the trace', () => {
    expect(traceOutcome([s('workflow_start'), s('so-sink'), s('router')], sinks).kind).toBe('no_sink')
  })
})

const wrap = (ui: React.ReactNode) => render(<MantineProvider>{ui}</MantineProvider>)

describe('TraceOutcomeBanner', () => {
  it('says the message was delivered and where', () => {
    wrap(<TraceOutcomeBanner outcome={{ kind: 'delivered', deliveredTo: ['Invoice log'] }} />)
    expect(screen.getByText('Delivered')).toBeInTheDocument()
    expect(screen.getByText(/Invoice log/)).toBeInTheDocument()
  })

  it('says where it failed', () => {
    wrap(<TraceOutcomeBanner outcome={{ kind: 'failed', deliveredTo: [], failedAt: { node_id: 'x', error: 'boom' } }} nodeName={() => 'Insert orders'} />)
    expect(screen.getByText('Failed')).toBeInTheDocument()
    expect(screen.getByText(/Insert orders/)).toBeInTheDocument()
    expect(screen.getByText(/boom/)).toBeInTheDocument()
  })
})

describe('TraceListStatus', () => {
  it('marks a trace with no failed step', () => {
    wrap(<TraceListStatus errorCount={0} />)
    expect(screen.getByText('No errors')).toBeInTheDocument()
  })
  it('marks a trace with a failed step', () => {
    wrap(<TraceListStatus errorCount={2} />)
    expect(screen.getByText('2 errors')).toBeInTheDocument()
  })
  it('shows nothing for a trace written before the count existed', () => {
    wrap(<TraceListStatus errorCount={undefined} />)
    expect(screen.queryByText('No errors')).not.toBeInTheDocument()
    expect(screen.queryByText(/error/)).not.toBeInTheDocument()
  })
})
