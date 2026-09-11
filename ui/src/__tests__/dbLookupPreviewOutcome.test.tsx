import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { VHostProvider } from '@/context/VHostContext'
import { server } from '../test/setupTests'
import { http, HttpResponse } from 'msw'
import { TransformationForm } from '@/components/forms/TransformationForm'
import { vi } from 'vitest'
import { signInAs } from '@/test/setupTests'

vi.mock('@tanstack/react-router', () => ({
  Link: (props: any) => <button {...props} />,
}))

// A db_lookup writes its row into one target field, and the preview panel used
// to render only the raw message JSON. Two things made that unreadable:
//
//  - a CDC sample comes back with the enriched field nested inside "after",
//    while every field picker in the editor shows the hoisted root path, so the
//    operator looked for "user_details" and saw "after";
//  - a lookup that finds no row passes the message through unchanged and
//    without an error, so a miss and a working lookup render identically.
//
// The panel therefore states the outcome for the node's target field.
describe('db_lookup preview outcome', () => {
  const setup = (data: any, incoming: any) => {
    const queryClient = new QueryClient()
    const selectedNode = { id: 'n1', type: 'transformation', data }
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
            />
          </VHostProvider>
        </QueryClientProvider>
      </MantineProvider>
    )
  }

  const config = { transType: 'db_lookup', targetField: 'user_details' }

  it('reports the target field value even when the result nests it under "after"', async () => {
    await signInAs('editor')
    server.use(
      http.post('/api/transformations/test', async () =>
        HttpResponse.json({
          operation: 'create',
          table: 'orders',
          after: { user_id: 1, user_details: { email: 'ada@example.com', name: 'Ada' } },
        })
      )
    )

    setup(config, { operation: 'create', table: 'orders', after: { user_id: 1 } })

    fireEvent.click(await screen.findByRole('button', { name: /run preview/i }))

    await waitFor(() => {
      expect(screen.getByTestId('preview-target-field')).toHaveTextContent('user_details')
    })
    expect(screen.getByTestId('preview-target-field')).toHaveTextContent('ada@example.com')
  })

  it('says so when the lookup produced nothing instead of showing an unchanged message', async () => {
    await signInAs('editor')
    server.use(
      // A miss: onMiss defaults to passthrough, so the endpoint answers 200
      // with the message exactly as it arrived.
      http.post('/api/transformations/test', async () => HttpResponse.json({ user_id: 999 }))
    )

    setup(config, { user_id: 999 })

    fireEvent.click(await screen.findByRole('button', { name: /run preview/i }))

    await waitFor(() => {
      expect(screen.getByTestId('preview-target-field')).toHaveTextContent(/not produced/i)
    })
  })
})
