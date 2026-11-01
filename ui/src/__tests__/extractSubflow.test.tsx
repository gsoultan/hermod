import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { server, signInAs } from '../test/setupTests'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'
import { ExtractSubflowButton } from '@/pages/workflows/WorkflowEditor/components/ExtractSubflow'

function seed(selected: string[]) {
  const n = (id: string, type: string, x: number) =>
    ({ id, type, position: { x, y: 0 }, selected: selected.includes(id), data: { label: id } })
  useWorkflowStore.setState({
    vhost: 'sales',
    nodes: [n('src', 'source', 0), n('a', 'transformation', 200), n('b', 'transformation', 400), n('out', 'sink', 600)],
    edges: [
      { id: 'e1', source: 'src', target: 'a' },
      { id: 'e2', source: 'a', target: 'b' },
      { id: 'e3', source: 'b', target: 'out' },
    ],
  } as any)
}

function renderButton() {
  return render(
    <MantineProvider>
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <ExtractSubflowButton />
      </QueryClientProvider>
    </MantineProvider>,
  )
}

describe('Turn selection into sub-flow', () => {
  it('is offered only for a selection a sub-flow can hold', () => {
    signInAs('Editor')
    seed(['src', 'a'])
    renderButton()
    expect(screen.queryByRole('button', { name: /into a sub-flow/i })).not.toBeInTheDocument()
  })

  it('saves the steps as a sub-flow and runs it from their place', async () => {
    signInAs('Editor')
    let saved: any = null
    server.use(
      http.post('/api/workflows', async ({ request }) => {
        saved = await request.json()
        return HttpResponse.json({ ...saved, id: saved.id }, { status: 201 })
      }),
    )
    seed(['a', 'b'])
    renderButton()

    await userEvent.click(screen.getByRole('button', { name: 'Turn 2 steps into a sub-flow' }))
    await userEvent.type(await screen.findByLabelText(/Sub-flow name/), 'Clean address')
    await userEvent.type(screen.getAllByPlaceholderText('customer.zip')[0], 'zip{enter}')
    await userEvent.click(screen.getByRole('button', { name: 'Create sub-flow' }))

    await waitFor(() => expect(saved).not.toBeNull())
    expect(saved).toMatchObject({ name: 'Clean address', vhost: 'sales' })
    expect(saved.nodes.map((n: any) => n.type)).toEqual(['subflow_input', 'transformation', 'transformation', 'subflow_return'])

    await waitFor(() => {
      const { nodes, edges } = useWorkflowStore.getState()
      expect(nodes.map((n) => n.type)).toEqual(['source', 'sink', 'call_subflow'])
      const call = nodes.find((n) => n.type === 'call_subflow')!
      expect(call.data).toMatchObject({ subflow_id: saved.id, version: 1, inputs: [{ from: 'zip', to: 'zip' }] })
      expect(edges.map((e) => `${e.source}>${e.target}`)).toEqual([`src>${call.id}`, `${call.id}>out`])
    })
  })
})
