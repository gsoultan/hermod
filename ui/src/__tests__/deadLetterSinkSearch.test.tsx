import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, vi } from 'vitest'
import { SidebarDrawer } from '@/pages/workflows/WorkflowEditor/components/SidebarDrawer'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'

const sinks = [
  { id: 's1', name: 'billing-dlq', type: 'postgres' },
  { id: 's2', name: 'orders-archive', type: 'kafka' },
  { id: 's3', name: 'audit-trail', type: 'mysql' },
]

describe('workflow settings: dead letter sink picker', () => {
  const initialState = useWorkflowStore.getState()

  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('[]', { status: 200, headers: { 'Content-Type': 'application/json' } })))
    useWorkflowStore.setState({ drawerOpened: true, drawerTab: 'settings', deadLetterSinkID: '' })
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    useWorkflowStore.setState(initialState, true)
  })

  const renderDrawer = () =>
    render(
      <MantineProvider>
        <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
          <SidebarDrawer onDragStart={() => {}} onAddItem={() => {}} sources={[]} sinks={sinks} />
        </QueryClientProvider>
      </MantineProvider>,
    )

  it('narrows the sinks by name as the user types', async () => {
    const user = userEvent.setup()
    renderDrawer()

    await user.type(screen.getByRole('combobox', { name: /dead letter sink/i }), 'bill')

    expect(await screen.findByRole('option', { name: /billing-dlq/i, hidden: true })).toBeInTheDocument()
    expect(screen.queryByRole('option', { name: /orders-archive/i, hidden: true })).not.toBeInTheDocument()
  })

  it('finds a sink by its type, and picking it sets the dead letter sink', async () => {
    const user = userEvent.setup()
    renderDrawer()

    await user.type(screen.getByRole('combobox', { name: /dead letter sink/i }), 'kafka')
    const option = await screen.findByRole('option', { name: /orders-archive/i, hidden: true })
    expect(screen.queryByRole('option', { name: /billing-dlq/i, hidden: true })).not.toBeInTheDocument()
    await user.click(option)

    expect(useWorkflowStore.getState().deadLetterSinkID).toBe('s2')
  })

  it('says so when nothing matches', async () => {
    const user = userEvent.setup()
    renderDrawer()

    await user.type(screen.getByRole('combobox', { name: /dead letter sink/i }), 'zzz')

    expect(await screen.findByText(/no sinks match/i)).toBeInTheDocument()
  })
})
