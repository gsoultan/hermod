import { Suspense } from 'react'
import { render, screen, fireEvent } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { vi } from 'vitest'
import { http, HttpResponse } from 'msw'
import { VHostProvider } from '@/context/VHostContext'
import { ConfirmProvider } from '@/components/common/ConfirmProvider'
import { SinkForm } from '@/components/forms/SinkForm'
import { server, signInAs } from '../test/setupTests'

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => () => {},
  Link: (props: any) => <button {...props} />,
}))

const empty = () => HttpResponse.json({ data: [], total: 0 })

function renderForm(props: { initialData?: any; isEditing: boolean; embedded?: boolean; vhost?: string }) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(
    <MantineProvider>
      <QueryClientProvider client={queryClient}>
        <VHostProvider>
          <ConfirmProvider>
            <Suspense fallback={<div>loading</div>}>
              <SinkForm embedded={props.embedded ?? true} initialData={props.initialData} isEditing={props.isEditing} vhost={props.vhost} />
            </Suspense>
          </ConfirmProvider>
        </VHostProvider>
      </QueryClientProvider>
    </MantineProvider>,
  )
}

/**
 * A sink that never names data_mode sends the whole row as one JSON string —
 * the shape it has always sent, which saved sinks and the apps reading them
 * depend on. It is also the shape that does not fit: FCM accepts 4096 bytes and
 * a wide row is more, so a new sink set up with the form's defaults failed on
 * its first real row. A sink created now starts with only the values listed,
 * written into its config, so the stored sink says what it does.
 */
describe('a new fcm sink', () => {
  beforeEach(() => {
    signInAs()
    server.use(
      http.get('/api/workers', empty),
      http.get('/api/vhosts', empty),
      http.get('/api/sinks', empty),
      http.get('/api/workflows', empty),
    )
  })

  it('starts by sending only the values listed', async () => {
    renderForm({ initialData: { name: 'push', type: 'fcm', vhost: '', config: {} }, isEditing: false })
    fireEvent.click(await screen.findByRole('button', { name: /next step/i }))

    expect(await screen.findByRole('radio', { name: /selected fields/i })).toBeChecked()
  })

  // The Add Sink page, where the type is picked rather than given.
  it('starts that way when FCM is picked as the type, too', async () => {
    renderForm({ isEditing: false, embedded: false, vhost: 'default' })

    fireEvent.change(screen.getByRole('textbox', { name: /^name/i }), { target: { value: 'push' } })
    fireEvent.click(screen.getByRole('combobox', { name: /^type/i }))
    fireEvent.click(await screen.findByRole('option', { name: 'Firebase (FCM)' }))
    fireEvent.click(await screen.findByRole('button', { name: /next step/i }))

    expect(await screen.findByRole('radio', { name: /selected fields/i })).toBeChecked()
  })

  // The default is for sinks made from now on. A saved sink that never named
  // data_mode has been sending the whole row, and opening it must not change that.
  it('leaves a saved sink sending what it always has', async () => {
    renderForm({
      initialData: { id: 's1', name: 'push', type: 'fcm', vhost: '', config: { credentials_json: '{}', topic: 'orders' } },
      isEditing: true,
    })
    fireEvent.click(await screen.findByRole('button', { name: /next step/i }))

    expect(await screen.findByRole('radio', { name: /whole row as json/i })).toBeChecked()
  })
})
