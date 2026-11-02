import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { SidebarDrawer } from '@/pages/workflows/WorkflowEditor/components/SidebarDrawer'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'

/**
 * Workflow settings: a workflow keeps the messages it cannot deliver on the
 * Dead Letters page, or leaves them on the source. There is no dead letter
 * sink to pick any more.
 */
describe('workflow settings: dead letters', () => {
  const initialState = useWorkflowStore.getState()

  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('[]', { status: 200, headers: { 'Content-Type': 'application/json' } })))
    useWorkflowStore.setState({ drawerOpened: true, drawerTab: 'settings' })
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    useWorkflowStore.setState(initialState, true)
  })

  const renderDrawer = () =>
    render(
      <MantineProvider>
        <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
          <SidebarDrawer onDragStart={() => {}} onAddItem={() => {}} sources={[]} sinks={[{ id: 's1', name: 'billing-dlq', type: 'postgres' }]} />
        </QueryClientProvider>
      </MantineProvider>,
    )

  it('keeps dead letters on the page by default, and offers no sink to pick', () => {
    expect(initialState.deadLetters).toBe(true)
    renderDrawer()

    expect(screen.getByRole('switch', { name: /keep failed messages on the dead letters page/i })).toBeChecked()
    expect(screen.queryByRole('combobox', { name: /dead letter sink/i })).not.toBeInTheDocument()
  })

  it('switching it off leaves failed messages on the source and turns off replay on startup', async () => {
    const user = userEvent.setup()
    useWorkflowStore.setState({ prioritizeDLQ: true })
    renderDrawer()

    const replay = screen.getByRole('checkbox', { name: /replay dead letters on startup/i })
    expect(replay).toBeEnabled()

    await user.click(screen.getByRole('switch', { name: /keep failed messages on the dead letters page/i }))

    expect(useWorkflowStore.getState().deadLetters).toBe(false)
    expect(screen.getByText(/stay on the source and are retried/i)).toBeInTheDocument()
    expect(screen.getByRole('checkbox', { name: /replay dead letters on startup/i })).toBeDisabled()
  })
})

describe('workflow settings: record key fields', () => {
  const initialState = useWorkflowStore.getState()

  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('[]', { status: 200, headers: { 'Content-Type': 'application/json' } })))
    useWorkflowStore.setState({ drawerOpened: true, drawerTab: 'settings' })
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    useWorkflowStore.setState(initialState, true)
  })

  // A payload that carries a timestamp or a signed link changes on every
  // read, so the fields that say which record it is have to be named for
  // repeated failures to count into one row.
  it('names the payload fields that identify a record, and keeps them in the store', async () => {
    const user = userEvent.setup()
    render(
      <MantineProvider>
        <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
          <SidebarDrawer onDragStart={() => {}} onAddItem={() => {}} sources={[]} sinks={[]} />
        </QueryClientProvider>
      </MantineProvider>,
    )
    const input = screen.getByRole('combobox', { name: /record key fields/i })
    await user.type(input, 'Application.application_id{enter}payload.DocumentTypeIds{enter}')
    expect(useWorkflowStore.getState().deadLetterKeyFields).toEqual(['Application.application_id', 'payload.DocumentTypeIds'])
  })

  it('is disabled when dead letters are off', () => {
    useWorkflowStore.setState({ deadLetters: false })
    render(
      <MantineProvider>
        <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
          <SidebarDrawer onDragStart={() => {}} onAddItem={() => {}} sources={[]} sinks={[]} />
        </QueryClientProvider>
      </MantineProvider>,
    )
    expect(screen.getByRole('combobox', { name: /record key fields/i })).toBeDisabled()
  })
})
