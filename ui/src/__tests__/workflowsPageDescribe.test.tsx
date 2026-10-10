import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { vi } from 'vitest'
import { VHostProvider } from '@/context/VHostContext'
import { ConfirmProvider } from '@/components/common/ConfirmProvider'
import { server, signInAs } from '../test/setupTests'
import { peekDraftWorkflow, clearDraftWorkflow } from '@/lib/draftWorkflow'
import WorkflowsPage from '../pages/workflows/WorkflowsPage'

const navigate = vi.fn()
vi.mock('@tanstack/react-router', () => ({
  Link: (props: any) => <button {...props} />,
  useNavigate: () => navigate,
}))

const drafted = { id: '', name: 'Drafted', vhost: 'default', active: false, nodes: [], edges: [] }

function renderPage() {
  server.use(
    http.get('/api/workflows', () => HttpResponse.json({ data: [], total: 0 })),
    http.get('/api/workspaces', () => HttpResponse.json([])),
    http.get('/api/workers', () => HttpResponse.json({ data: [], total: 0 })),
    http.get('/api/vhosts/:vhost/secrets', () => HttpResponse.json({ data: [] })),
    http.post('/api/ai/build-workflow', () =>
      HttpResponse.json({ workflow: drafted, issues: [], saved: false, provider: 'ollama', model: 'llama3', usage: { input_tokens: 1, output_tokens: 1 } }),
    ),
  )
  render(
    <MantineProvider>
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })}>
        <VHostProvider>
          <ConfirmProvider>
            <WorkflowsPage />
          </ConfirmProvider>
        </VHostProvider>
      </QueryClientProvider>
    </MantineProvider>,
  )
}

describe('Workflows page: Describe an automation', () => {
  beforeEach(() => {
    navigate.mockReset()
    clearDraftWorkflow()
  })

  it('opens a draft in the editor as a new workflow without saving it', async () => {
    let saves = 0
    server.use(http.post('/api/workflows', () => { saves += 1; return HttpResponse.json({}) }))
    signInAs('Editor')
    const user = userEvent.setup()
    renderPage()

    await user.click(await screen.findByRole('button', { name: /describe an automation/i }))
    fireEvent.change(await screen.findByRole('textbox', { name: /describe the automation/i }), { target: { value: 'Copy orders' } })
    await user.click(screen.getByRole('combobox', { name: /^provider/i }))
    await user.click(await screen.findByRole('option', { name: 'Ollama (local)', hidden: true }))
    fireEvent.change(screen.getByRole('textbox', { name: /^model/i }), { target: { value: 'llama3' } })
    await user.click(screen.getByRole('button', { name: /^draft workflow/i }))
    await user.click(await screen.findByRole('button', { name: /open in editor/i }))

    expect(navigate).toHaveBeenCalledWith({ to: '/workflows/new' })
    expect(peekDraftWorkflow()).toEqual(drafted)
    expect(saves).toBe(0)
  })

  it('is not offered to a Viewer', async () => {
    signInAs('Viewer')
    renderPage()
    await screen.findByText(/no workflows found/i)
    expect(screen.queryByRole('button', { name: /describe an automation/i })).toBeNull()
  })
})
