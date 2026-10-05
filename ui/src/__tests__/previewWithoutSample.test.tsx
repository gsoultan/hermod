import { render, screen, within, fireEvent } from '@testing-library/react'
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

// A node fed by a queue has no sample until someone asks for one: the editor
// never reads a queue on its own. With no sample there is nothing to run the
// node on, and Run Preview returned without doing or saying anything -- the
// panel stayed on "// No preview yet" however many times it was pressed. The
// reason was written in another column of the drawer, beside a refresh icon.
//
// Reported as "Run Preview shows no returned value" for an execute_sql node
// behind a RabbitMQ queue. The panel the button is in now says why, and offers
// the one action that fixes it.
describe('live preview with no sample to run on', () => {
  const setup = (incoming: any, onRefreshFields?: () => void) => {
    const queryClient = new QueryClient()
    const selectedNode = { id: 'n1', type: 'transformation', data: { transType: 'execute_sql' } }
    render(
      <MantineProvider>
        <QueryClientProvider client={queryClient}>
          <VHostProvider>
            <TransformationForm
              selectedNode={selectedNode as any}
              updateNodeConfig={() => {}}
              availableFields={[]}
              incomingPayload={incoming}
              sinkSchema={{}}
              onRefreshFields={onRefreshFields}
            />
          </VHostProvider>
        </QueryClientProvider>
      </MantineProvider>
    )
  }

  it('says why, where the button is, and fetches a sample when asked', async () => {
    const onRefreshFields = vi.fn()
    setup(undefined, onRefreshFields)

    const panel = await screen.findByTestId('live-preview')
    const notice = await within(panel).findByTestId('preview-no-input')
    expect(notice).toHaveTextContent(/no sample/i)

    // A button that cannot do anything does not pretend it can.
    expect(within(panel).getByRole('button', { name: /run preview/i })).toBeDisabled()

    fireEvent.click(within(notice).getByRole('button', { name: /fetch a sample/i }))
    expect(onRefreshFields).toHaveBeenCalledTimes(1)
  })

  it('does not offer a fetch nothing is wired to do', async () => {
    setup(undefined)

    const panel = await screen.findByTestId('live-preview')
    const notice = await within(panel).findByTestId('preview-no-input')
    expect(within(notice).queryByRole('button', { name: /fetch a sample/i })).toBeNull()
  })

  it('leaves the panel alone once there is a sample', async () => {
    server.use(
      http.post('*/api/transformations/test', () => HttpResponse.json({ code: 'MQ-1' }))
    )
    setup({ code: 'MQ-1' }, vi.fn())

    const panel = await screen.findByTestId('live-preview')
    expect(within(panel).queryByTestId('preview-no-input')).toBeNull()
    expect(within(panel).getByRole('button', { name: /run preview/i })).toBeEnabled()
  })
})
