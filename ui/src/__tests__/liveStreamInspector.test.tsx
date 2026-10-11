import { render, screen, act, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { vi, afterEach, beforeEach } from 'vitest'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'
import { LiveStreamInspector } from '@/pages/workflows/WorkflowEditor/components/LiveStreamInspector'

// jsdom has no Worker; filter on the main thread with the same rule the
// worker applies to node_id. One function for the life of the test, as the
// real hook's is, or the inspector's filter effect re-runs every render.
vi.mock('@/hooks/useDataWorker', () => {
  const process = async (_type: string, data: any[], params: any) => {
    const value = params?.filters?.[0]?.value
    return value && value !== 'all' ? data.filter((m) => m.node_id === value) : data
  }
  return { useDataWorker: () => ({ processing: false, process }) }
})

class FakeSocket {
  static instances: FakeSocket[] = []
  onmessage: ((e: any) => void) | null = null
  constructor(public url: string) {
    FakeSocket.instances.push(this)
  }
  close() {}
}

const initialState = useWorkflowStore.getState()

beforeEach(() => {
  FakeSocket.instances = []
  vi.stubGlobal('WebSocket', FakeSocket as any)
  useWorkflowStore.setState({
    nodes: [
      { id: 'src', type: 'source', position: { x: 0, y: 0 }, data: { label: 'Orders' } },
      { id: 'snk', type: 'sink', position: { x: 0, y: 0 }, data: { label: 'Warehouse' } },
    ] as any,
  })
})
afterEach(() => {
  vi.unstubAllGlobals()
  useWorkflowStore.setState(initialState, true)
})

const renderInspector = () =>
  render(
    <MantineProvider>
      <LiveStreamInspector opened onClose={() => {}} workflowId="wf-1" />
    </MantineProvider>,
  )

describe('live stream inspector', () => {
  // The node filter was a row of click-only badges: not reachable with Tab,
  // not pressable, and nothing said which one was chosen.
  it('filters by node from the keyboard and says which filter is on', async () => {
    const user = userEvent.setup()
    renderInspector()

    const warehouse = await screen.findByRole('button', { name: 'Warehouse' })
    expect(screen.getByRole('button', { name: 'All Nodes' })).toHaveAttribute('aria-pressed', 'true')
    expect(warehouse).toHaveAttribute('aria-pressed', 'false')

    warehouse.focus()
    await user.keyboard('{Enter}')
    expect(screen.getByRole('button', { name: 'Warehouse' })).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByRole('button', { name: 'All Nodes' })).toHaveAttribute('aria-pressed', 'false')
  })

  // With messages arriving but a filter hiding all of them, the panel said
  // "No live messages yet. Start the workflow" — about a workflow that was
  // running and sending.
  it('says a filter is hiding messages rather than that none have arrived', async () => {
    const user = userEvent.setup()
    renderInspector()
    await waitFor(() => expect(FakeSocket.instances.length).toBeGreaterThan(0))
    act(() => {
      FakeSocket.instances.at(-1)!.onmessage?.({
        data: JSON.stringify({ workflow_id: 'wf-1', node_id: 'src', timestamp: '2026-10-09T10:00:00Z', data: { id: 1 } }),
      })
    })
    expect(await screen.findByText(/"id": 1/)).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Warehouse' }))

    expect(await screen.findByText(/no messages match this filter/i)).toBeInTheDocument()
    expect(screen.queryByText(/no live messages yet/i)).toBeNull()
  })
})
