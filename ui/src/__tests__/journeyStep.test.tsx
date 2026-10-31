import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { JourneyStep } from '@/pages/workflows/traces/JourneyStep'

const step = {
  node_id: 'n1',
  timestamp: '2026-10-31T06:00:00Z',
  duration_ms: 4,
  before: { stage: 'in' },
  after: { stage: 'out' },
}

const renderStep = (compact: boolean) =>
  render(<MantineProvider><JourneyStep step={step} nodeName="Orders" compact={compact} /></MantineProvider>)

describe('JourneyStep', () => {
  it('shows input and output side by side when there is room', () => {
    renderStep(false)
    expect(screen.getByText('INPUT / BEFORE')).toBeInTheDocument()
    expect(screen.getByText(/"stage": "in"/)).toBeInTheDocument()
    expect(screen.getByText(/"stage": "out"/)).toBeInTheDocument()
  })

  // On a phone the two payloads stacked made each step three screens tall.
  it('on a phone shows one payload at a time, output first', async () => {
    renderStep(true)
    expect(screen.queryByText('INPUT / BEFORE')).not.toBeInTheDocument()
    expect(screen.getByText(/"stage": "out"/)).toBeInTheDocument()
    expect(screen.queryByText(/"stage": "in"/)).not.toBeInTheDocument()
    await userEvent.click(screen.getByText('Input'))
    expect(screen.getByText(/"stage": "in"/)).toBeInTheDocument()
    expect(screen.queryByText(/"stage": "out"/)).not.toBeInTheDocument()
  })

  it('names the node and keeps its internal id', () => {
    renderStep(false)
    expect(screen.getByText('Node: Orders')).toBeInTheDocument()
    expect(screen.getByText('n1')).toBeInTheDocument()
  })
})
