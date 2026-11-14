import { render, screen } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { vi } from 'vitest'
import { LiveLogPanel } from '@/pages/workflows/WorkflowEditor/components/LiveLogPanel'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'

describe('Live log row markup', () => {
  // A line that carries a trace shows a "Trace" badge after its message. The
  // badge is a block element; inside the message's <p> it is invalid HTML and
  // React logs it for every such line.
  it('renders a traced line without nesting a block inside a paragraph', () => {
    Element.prototype.scrollTo = vi.fn() as any // jsdom has none
    const error = vi.spyOn(console, 'error').mockImplementation(() => {})
    useWorkflowStore.setState({
      logsOpened: true,
      logs: [{ timestamp: '2026-01-01T00:00:00Z', level: 'INFO', message: 'delivered', data: 'msg-1' } as any],
    })
    render(
      <MantineProvider>
        <LiveLogPanel workflowId="wf" active onTraceClick={vi.fn()} onErrorClick={vi.fn()} />
      </MantineProvider>,
    )
    expect(screen.getByText('Trace')).toBeInTheDocument()
    const nesting = error.mock.calls.filter((args) => /cannot be a descendant|cannot contain a nested/.test(String(args[0])))
    error.mockRestore()
    expect(nesting).toEqual([])
  })
})
