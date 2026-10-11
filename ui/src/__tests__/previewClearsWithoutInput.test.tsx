import { render, screen, within, fireEvent, waitFor } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { VHostProvider } from '@/context/VHostContext'
import { server, signInAs } from '../test/setupTests'
import { http, HttpResponse } from 'msw'
import { TransformationForm } from '@/components/forms/TransformationForm'
import { vi } from 'vitest'

vi.mock('@tanstack/react-router', () => ({
  Link: (props: any) => <button {...props} />,
}))

/**
 * The Live Preview kept the last result it showed after the message it ran on
 * was gone, so a node with nothing to run on still showed output, from an
 * input that no longer existed. With no input the preview now starts over.
 */
describe('live preview when its input goes away', () => {
  const queryClient = new QueryClient()
  const selectedNode = { id: 'n1', type: 'transformation', data: { transType: 'set' } }
  const view = (incoming: any) => (
    <MantineProvider>
      <QueryClientProvider client={queryClient}>
        <VHostProvider>
          <TransformationForm
            selectedNode={selectedNode as any}
            updateNodeConfig={() => {}}
            availableFields={[]}
            incomingPayload={incoming}
            sinkSchema={{}}
          />
        </VHostProvider>
      </QueryClientProvider>
    </MantineProvider>
  )

  it('drops the last result and says there is no sample', async () => {
    signInAs()
    server.use(
      http.post('*/api/transformations/test', () => HttpResponse.json({ previewed_value: 'from-old-input' }))
    )
    const { rerender } = render(view({ order_id: 1 }))

    const panel = await screen.findByTestId('live-preview')
    fireEvent.click(within(panel).getByRole('button', { name: /run preview/i }))
    await waitFor(() => expect(panel).toHaveTextContent('from-old-input'))

    rerender(view(undefined))

    await waitFor(() => expect(within(panel).getByTestId('preview-no-input')).toBeInTheDocument())
    expect(panel).not.toHaveTextContent('from-old-input')
  }, 15000)
})
