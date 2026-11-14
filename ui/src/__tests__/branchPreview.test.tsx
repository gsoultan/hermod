import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { VHostProvider } from '@/context/VHostContext'
import { server, signInAs } from '../test/setupTests'
import { http, HttpResponse } from 'msw'
import { TransformationForm } from '@/components/forms/TransformationForm'
import { PreviewPanel } from '@/components/workflow/Transformation/PreviewPanel'
import { vi } from 'vitest'

vi.mock('@tanstack/react-router', () => ({
  Link: (props: any) => <button {...props} />,
}))

// An If node's Live Preview never said which way the message went.
//
// The engine answers a routing node's preview with {branch, result}. The form
// kept `result` and dropped `branch`, so the panel compared the message with
// itself, found it unchanged -- an If node never changes the message -- and
// told the user "the configuration is not matching your data". A condition
// such as array_len(source.users) > 0 that held (branch "true") therefore read
// as a condition that failed, and nothing on screen said otherwise.

const UNCHANGED = /returned the message unchanged/i

const renderPanel = (props: Record<string, unknown>) =>
  render(
    <MantineProvider>
      <PreviewPanel title="LIVE PREVIEW" onRun={() => {}} {...props} />
    </MantineProvider>,
  )

describe('the preview of a routing node shows the branch it took', () => {
  it('names the branch', () => {
    const message = { users: [{ id: 1 }] }
    renderPanel({ original: message, result: { ...message }, branch: 'true' })
    expect(screen.getByTestId('preview-branch')).toHaveTextContent(/branch taken/i)
    expect(screen.getByTestId('preview-branch')).toHaveTextContent('true')
  })

  it('does not call an unchanged message a mismatch, since routing never changes it', () => {
    const message = { users: [{ id: 1 }] }
    renderPanel({ original: message, result: { ...message }, branch: 'false' })
    expect(screen.queryByText(UNCHANGED)).not.toBeInTheDocument()
  })

  it('still reports an unchanged message for a node that is not routing', () => {
    const message = { users: [{ id: 1 }] }
    renderPanel({ original: message, result: { ...message } })
    expect(screen.getByText(UNCHANGED)).toBeInTheDocument()
    expect(screen.queryByTestId('preview-branch')).not.toBeInTheDocument()
  })
})

describe('an If node in the editor', () => {
  const setup = (nodeType: string, data: Record<string, unknown>) => {
    const queryClient = new QueryClient()
    render(
      <MantineProvider>
        <QueryClientProvider client={queryClient}>
          <VHostProvider>
            <TransformationForm
              selectedNode={{ id: 'n-if', type: nodeType, data } as any}
              updateNodeConfig={() => {}}
              availableFields={[]}
              incomingPayload={{ users: [{ id: 1 }] }}
              sinkSchema={{}}
            />
          </VHostProvider>
        </QueryClientProvider>
      </MantineProvider>,
    )
  }

  for (const branch of ['true', 'false']) {
    it(`shows the engine's "${branch}" for a condition on a function result`, async () => {
      signInAs()
      server.use(
        http.post('/api/transformations/test', async () =>
          HttpResponse.json({ branch, result: { users: [{ id: 1 }] } }),
        ),
      )
      setup('condition', {
        conditions: [{ field: 'array_len(source.users)', operator: '>', value: '0' }],
      })
      fireEvent.click(await screen.findByRole('button', { name: /run preview/i }))
      await waitFor(() => expect(screen.getByTestId('preview-branch')).toHaveTextContent(branch))
      expect(screen.queryByText(UNCHANGED)).not.toBeInTheDocument()
    })
  }

  it('shows the case a Switch picked', async () => {
    signInAs()
    server.use(
      http.post('/api/transformations/test', async () =>
        HttpResponse.json({ branch: 'has-users', result: { users: [{ id: 1 }] } }),
      ),
    )
    setup('switch', {
      field: 'array_len(source.users)',
      cases: [{ label: 'has-users', operator: '>', value: '0' }],
    })
    fireEvent.click(await screen.findByRole('button', { name: /run preview/i }))
    await waitFor(() => expect(screen.getByTestId('preview-branch')).toHaveTextContent('has-users'))
  })
})
