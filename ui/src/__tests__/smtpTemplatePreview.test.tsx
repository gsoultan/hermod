import { Suspense } from 'react'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { VHostProvider } from '@/context/VHostContext'
import { vi } from 'vitest'
import { SinkForm } from '@/components/forms/SinkForm'
import { http, HttpResponse } from 'msw'
import { server, signInAs } from '../test/setupTests'
import { ConfirmProvider } from '@/components/common/ConfirmProvider'
import { SMTPSinkConfig } from '@/components/workflow/Sink/SMTPSinkConfig'

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => () => {},
  Link: (props: any) => <button {...props} />,
}))

// The Preview button was gated on a prop nobody passed, and the endpoint behind
// it was an empty stub, so an operator could write a template and had no way to
// see it render short of activating the workflow and mailing someone.
describe('SMTP template preview', () => {
  const setup = (config: Record<string, string>, incomingPayload?: any) =>
    render(
      <MantineProvider>
        <ConfirmProvider>
          <SMTPSinkConfig config={config} updateConfig={() => {}} incomingPayload={incomingPayload} />
        </ConfirmProvider>
      </MantineProvider>
    )

  it('renders the template through the server and shows what would be sent', async () => {
    let posted: any = null
    server.use(
      http.post('/api/sinks/smtp/preview', async ({ request }) => {
        posted = await request.json()
        return HttpResponse.json({
          subject: 'Order 42 on 01 Dec 2026',
          from: 'ops@example.com',
          to: ['buyer@example.com'],
          body: '<p>Hi Ana</p>',
          html: true,
          sample: { id: '42', name: 'Ana' },
        })
      })
    )

    setup({ template: '<p>Hi {{.name}}</p>', subject: 'Order {{.id}}' })
    fireEvent.click(screen.getByRole('button', { name: /preview template/i }))

    await waitFor(() => expect(screen.getByText(/Order 42 on 01 Dec 2026/)).toBeTruthy())
    expect(screen.getByText(/buyer@example.com/)).toBeTruthy()
    // The sink's own config is what gets rendered, not a re-typed copy.
    expect(posted.type).toBe('smtp')
    expect(posted.config.template).toBe('<p>Hi {{.name}}</p>')
    // The row that was rendered comes back, so it can be edited into a real one.
    await waitFor(() =>
      expect((screen.getByRole('textbox', { name: /Sample row/i }) as HTMLTextAreaElement).value).toContain('Ana')
    )
  })

  it('shows the template error in the modal instead of a rendered email', async () => {
    server.use(
      http.post('/api/sinks/smtp/preview', () =>
        HttpResponse.json({ error: 'failed to set body: parse html template: unclosed action' }, { status: 400 })
      )
    )

    setup({ template: '<p>{{ .name </p>' })
    fireEvent.click(screen.getByRole('button', { name: /preview template/i }))

    await waitFor(() => expect(screen.getByText(/unclosed action/)).toBeTruthy())
    expect(screen.queryByTitle('Rendered email')).toBeNull()
  })

  it('refuses a sample row that is not JSON without asking the server', async () => {
    let called = false
    server.use(
      http.post('/api/sinks/smtp/preview', () => {
        called = true
        return HttpResponse.json({
          subject: 's', from: 'f@example.com', to: ['t@example.com'],
          body: 'b', html: false, sample: {},
        })
      })
    )

    setup({ template: 'hello' })
    fireEvent.click(screen.getByRole('button', { name: /preview template/i }))
    await waitFor(() => expect(called).toBe(true))

    called = false
    // The modal's transition means the field arrives a tick after the request.
    const sampleField = await screen.findByRole('textbox', { name: /Sample row/i })
    fireEvent.change(sampleField, { target: { value: '{not json' } })
    fireEvent.click(screen.getByRole('button', { name: /^render$/i }))

    await waitFor(() => expect(screen.getByText(/not valid JSON/i)).toBeTruthy())
    expect(called).toBe(false)
  })

// The editor already knows the row that reaches this sink — it is what the
// field picker is built from. Rendering the preview against a made-up example
// when the real one is in hand is a preview of the wrong thing.
  it('renders against the row the editor says flows in', async () => {
    let posted: any = null
    server.use(
      http.post('/api/sinks/smtp/preview', async ({ request }) => {
        posted = await request.json()
        return HttpResponse.json({
          subject: 'Order 7',
          from: 'ops@example.com',
          to: ['real@example.com'],
          body: 'rendered',
          html: false,
          sample: { id: '7', email: 'real@example.com' },
        })
      })
    )

    setup({ template: 'Order {{.id}}' }, { id: '7', email: 'real@example.com' })
    fireEvent.click(screen.getByRole('button', { name: /preview template/i }))

    await waitFor(() => expect(posted).not.toBeNull())
    expect(posted.sample).toEqual({ id: '7', email: 'real@example.com' })
    const box = await screen.findByRole('textbox', { name: /Sample row/i })
    expect((box as HTMLTextAreaElement).value).toContain('real@example.com')
  })
})

// The prop that carries the editor's row was declared on SinkForm and never
// destructured, so it stopped there: the sink forms could not see the sample
// the editor had already fetched. A test on the SMTP form alone cannot catch
// that, because the form is not where it was dropped.
describe('the editor row reaches the sink form', () => {
  beforeEach(() => {
    signInAs()
  })

  it('previews against the row SinkForm was handed', async () => {
    let posted: any = null
    server.use(
      http.get('/api/workers', () => HttpResponse.json({ data: [], total: 0 })),
      http.get('/api/vhosts', () => HttpResponse.json({ data: [], total: 0 })),
      http.get('/api/sinks', () => HttpResponse.json({ data: [], total: 0 })),
      http.get('/api/workflows', () => HttpResponse.json({ data: [], total: 0 })),
      http.post('/api/sinks/smtp/preview', async ({ request }) => {
        posted = await request.json()
        return HttpResponse.json({
          subject: 'hi', from: 'ops@example.com', to: ['real@example.com'],
          body: 'rendered', html: false, sample: { id: '7' },
        })
      })
    )

    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    })
    render(
      <MantineProvider>
        <QueryClientProvider client={queryClient}>
          <VHostProvider>
            <ConfirmProvider>
              <Suspense fallback={<div>loading</div>}>
                <SinkForm
                  embedded
                  isEditing
                  initialData={{
                    id: 's1', name: 'mailer', type: 'smtp', vhost: '',
                    config: { template: 'Order {{.id}}' },
                  } as any}
                  incomingPayload={{ id: '7', email: 'real@example.com' }}
                />
              </Suspense>
            </ConfirmProvider>
          </VHostProvider>
        </QueryClientProvider>
      </MantineProvider>
    )

    fireEvent.click(await screen.findByRole('button', { name: /next step/i }))
    fireEvent.click(await screen.findByRole('button', { name: /preview template/i }))

    await waitFor(() => expect(posted).not.toBeNull())
    expect(posted.sample).toEqual({ id: '7', email: 'real@example.com' })
  })
})
