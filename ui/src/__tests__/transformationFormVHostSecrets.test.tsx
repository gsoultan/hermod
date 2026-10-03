import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { vi } from 'vitest'
import { server, signInAs } from '../test/setupTests'
import { VHostProvider } from '@/context/VHostContext'
import { TransformationForm } from '@/components/forms/TransformationForm'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'

vi.mock('@tanstack/react-router', () => ({
  Link: (props: any) => <button {...props} />,
}))

/**
 * A node's preview is answered for the vhost of the workflow being edited:
 * that is whose secrets secret("NAME") reads. The form has to say which vhost
 * that is, and it offers that vhost's secret names where a value is written.
 */

function renderSetNode() {
  signInAs('Editor')
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  render(
    <MantineProvider>
      <QueryClientProvider client={qc}>
        <VHostProvider>
          <TransformationForm
            selectedNode={{ id: 'n1', type: 'transformation', data: { transType: 'set', 'column.key': '' } } as any}
            updateNodeConfig={() => {}}
            availableFields={[]}
            incomingPayload={{ id: 1 }}
            sinkSchema={{}}
          />
        </VHostProvider>
      </QueryClientProvider>
    </MantineProvider>,
  )
}

describe('a node form in a vhost', () => {
  beforeEach(() => {
    useWorkflowStore.setState({ vhost: 'tenant-a' })
  })
  afterEach(() => {
    useWorkflowStore.setState({ vhost: 'default' })
  })

  it('previews as the workflow’s vhost', async () => {
    const sent: any[] = []
    server.use(
      http.get('/api/vhosts/:vhost/secrets', () => HttpResponse.json({ data: [], total: 0 })),
      http.post('/api/transformations/test', async ({ request }) => {
        sent.push(await request.json())
        return HttpResponse.json({ id: 1 })
      }),
    )
    renderSetNode()

    await waitFor(() => expect(sent.length).toBeGreaterThan(0), { timeout: 5000 })
    expect(sent[0].vhost).toBe('tenant-a')
  }, 20000)

  it('offers the vhost’s secrets in a row’s value, as an expression', async () => {
    const asked: string[] = []
    server.use(
      http.get('/api/vhosts/:vhost/secrets', ({ params }) => {
        asked.push(params.vhost as string)
        return HttpResponse.json({ data: [{ name: 'API_KEY', updated_by: 'ada', updated_at: '' }], total: 1 })
      }),
      http.post('/api/transformations/test', () => HttpResponse.json({ id: 1 })),
    )
    const user = userEvent.setup()
    renderSetNode()

    await user.click(await screen.findByRole('button', { name: /insert variable/i }, { timeout: 5000 }))
    const picker = await screen.findByRole('dialog', { hidden: true }, { timeout: 5000 })
    const secrets = await within(picker).findByRole('group', { name: /secrets/i, hidden: true })
    expect(within(secrets).getByText('API_KEY')).toBeInTheDocument()
    expect(asked).toContain('tenant-a')
  }, 20000)
})
