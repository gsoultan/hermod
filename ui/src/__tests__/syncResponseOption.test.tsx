import { render, screen, fireEvent } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { vi } from 'vitest'
import { OtherSourceConfig } from '@/components/workflow/Source/OtherSourceConfig'

/**
 * A webhook or gRPC source answered its caller as soon as the record was
 * queued, and there was no way to ask for anything else. The form now offers
 * the choice: answer at once, or hold the caller until the workflow has
 * finished and answer with what happened. A WebSocket source, which reads
 * frames from a server it dials, has the same choice: read only, or write a
 * result frame back for each frame read.
 *
 * The keys written are the ones the transports read — `response_mode` and
 * `response_timeout` (pkg/comm/reply). A form that wrote any other key would
 * save, and the source would go on answering "dispatched".
 */
function renderConfig(sourceType: string, config: Record<string, any>) {
  const updateConfig = vi.fn()
  render(
    <MantineProvider>
      <OtherSourceConfig config={config} updateConfig={updateConfig} sourceType={sourceType} />
    </MantineProvider>,
  )
  return updateConfig
}

describe.each(['webhook', 'grpc', 'websocket'])('the response option on a %s source', (sourceType) => {
  it('is asynchronous unless the source says otherwise', () => {
    renderConfig(sourceType, { path: '/p' })
    expect(screen.getByRole('radio', { name: 'Asynchronous' })).toBeChecked()
    expect(screen.getByRole('radio', { name: 'Synchronous' })).not.toBeChecked()
    expect(screen.queryByLabelText(/Response timeout/i)).not.toBeInTheDocument()
  })

  it('writes response_mode when synchronous is chosen', () => {
    const updateConfig = renderConfig(sourceType, { path: '/p' })
    fireEvent.click(screen.getByRole('radio', { name: 'Synchronous' }))
    expect(updateConfig).toHaveBeenCalledWith('response_mode', 'sync')
  })

  it('shows the timeout, and what the caller gets back, once it is synchronous', () => {
    const updateConfig = renderConfig(sourceType, { path: '/p', response_mode: 'sync', response_timeout: '10s' })
    expect(screen.getByRole('radio', { name: 'Synchronous' })).toBeChecked()

    const timeout = screen.getByLabelText(/Response timeout/i)
    expect(timeout).toHaveValue('10s')
    fireEvent.change(timeout, { target: { value: '45s' } })
    expect(updateConfig).toHaveBeenCalledWith('response_timeout', '45s')

    // The statuses a caller has to handle, named where the choice is made.
    const explanation = screen.getByTestId('sync-response-explanation')
    for (const status of ['delivered', 'completed', 'dead_lettered', 'failed', 'pending']) {
      expect(explanation).toHaveTextContent(status)
    }
  })
})

describe('the response option', () => {
  it('is not offered on a form source, which answers a browser', () => {
    renderConfig('form', { path: '/api/forms/x' })
    expect(screen.queryByRole('radio', { name: 'Synchronous' })).not.toBeInTheDocument()
  })

  it('changes what the gRPC guide says the reply means', () => {
    renderConfig('grpc', { path: '/grpc/orders', response_mode: 'sync' })
    const points = screen.getAllByRole('listitem').map((item) => item.textContent ?? '')
    expect(points.some((text) => /waits for the workflow/i.test(text))).toBe(true)
    expect(points.some((text) => /does not wait for transformations or sinks/i.test(text))).toBe(false)
  })
})
