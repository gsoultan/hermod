import { render, screen, within, fireEvent, waitFor } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { VHostProvider } from '@/context/VHostContext'
import { server } from '../test/setupTests'
import { http, HttpResponse } from 'msw'
import { TransformationForm } from '@/components/forms/TransformationForm'
import { vi } from 'vitest'

vi.mock('@tanstack/react-router', () => ({
  Link: (props: any) => <button {...props} />,
}))

// Live Preview re-runs a node shortly after every change to its config, and a
// preview runs the node for real. For an execute_sql node that meant the
// statement was executed against the database at every pause in typing: a
// finished INSERT wrote a row each time, and an unfinished one raised two error
// toasts. A node that writes previews when it is asked to.
describe('execute_sql live preview', () => {
  const setup = (data: any, incoming: any = { code: 'C-1' }) => {
    const calls: any[] = []
    server.use(
      http.post('*/api/transformations/test', async ({ request }) => {
        calls.push(await request.json())
        return HttpResponse.json({ code: 'C-1', inserted: { id: 1 } })
      })
    )
    render(
      <MantineProvider>
        <QueryClientProvider client={new QueryClient()}>
          <VHostProvider>
            <TransformationForm
              selectedNode={{ id: 'n1', type: 'transformation', data } as any}
              updateNodeConfig={() => {}}
              availableFields={[]}
              incomingPayload={incoming}
              sinkSchema={{}}
            />
          </VHostProvider>
        </QueryClientProvider>
      </MantineProvider>
    )
    return calls
  }

  const wait = (ms: number) => new Promise((r) => setTimeout(r, ms))

  it('does not run the statement on its own', async () => {
    const calls = setup({
      transType: 'execute_sql', sourceId: 'ops',
      queryTemplate: 'INSERT INTO t (code) VALUES ({{.code}}) RETURNING id', resultField: 'inserted',
    })
    await screen.findByTestId('live-preview')
    await wait(900)
    expect(calls).toHaveLength(0)
  })

  it('runs it once when Run Preview is pressed', async () => {
    const calls = setup({
      transType: 'execute_sql', sourceId: 'ops',
      queryTemplate: 'INSERT INTO t (code) VALUES ({{.code}}) RETURNING id', resultField: 'inserted',
    })
    const panel = await screen.findByTestId('live-preview')
    fireEvent.click(within(panel).getByRole('button', { name: /run preview/i }))
    await waitFor(() => expect(calls).toHaveLength(1))
    expect(await within(panel).findByText(/"inserted"/)).toBeInTheDocument()
    await wait(900)
    expect(calls).toHaveLength(1)
  })

  it('tells the reader why nothing ran', async () => {
    setup({ transType: 'execute_sql', sourceId: 'ops', queryTemplate: 'DELETE FROM t' })
    expect(await screen.findByTestId('execute-sql-manual-preview')).toHaveTextContent(/run preview/i)
  })

  // With no sample the panel offers to fetch one. For every other node the
  // preview then runs by itself; saying so here would promise a run that this
  // node, on purpose, does not make.
  it('does not promise a run when it offers to fetch a sample', async () => {
    setup({ transType: 'execute_sql', sourceId: 'ops', queryTemplate: 'DELETE FROM t' }, null)
    const notice = await screen.findByTestId('preview-no-input')
    expect(notice).toHaveTextContent(/then press run preview/i)
    expect(notice).not.toHaveTextContent(/the preview runs on it/i)
  })

  it('keeps that promise for nodes that do run on their own', async () => {
    setup({ transType: 'mask', field: 'code', maskType: 'all' }, null)
    expect(await screen.findByTestId('preview-no-input')).toHaveTextContent(/the preview runs on it/i)
  })

  // The control: the same harness does see a preview from a node that only
  // reads the message.
  it('still previews other nodes as they are edited', async () => {
    const calls = setup({ transType: 'mask', field: 'code', maskType: 'all' })
    await screen.findByTestId('live-preview')
    await waitFor(() => expect(calls.length).toBeGreaterThan(0), { timeout: 3000 })
  })
})
