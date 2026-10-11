import { render, screen, fireEvent } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { QueueSinkConfig } from '@/components/workflow/Sink/QueueSinkConfig'

// The Kafka sink writes each batch as one transaction when it has a
// transactional ID. The setting had no field, so exactly-once was reachable
// only by hand-editing a workflow's JSON.
describe('kafka sink transactions', () => {
  const setup = (config: Record<string, string> = {}) => {
    const updateConfig = vi.fn()
    render(
      <MantineProvider>
        <QueueSinkConfig type="kafka" config={config} updateConfig={updateConfig} />
      </MantineProvider>
    )
    return updateConfig
  }

  it('offers a transactional ID and writes it to transactional_id', () => {
    const updateConfig = setup()
    fireEvent.change(screen.getByLabelText(/transactional id/i), { target: { value: 'hermod-orders' } })
    expect(updateConfig).toHaveBeenCalledWith('transactional_id', 'hermod-orders')
  })

  it('shows the saved transactional ID', () => {
    setup({ transactional_id: 'hermod-orders' })
    expect((screen.getByLabelText(/transactional id/i) as HTMLInputElement).value).toBe('hermod-orders')
  })

  // A transaction commits the source's offsets with the output. That is only
  // right when this sink is the workflow's only output, so the form says so
  // once the setting is on, and not before.
  it('does not warn while transactions are off', () => {
    setup()
    expect(screen.queryByText(/only sink/i)).toBeNull()
  })

  it('warns about other sinks when transactions are on', () => {
    setup({ transactional_id: 'hermod-orders' })
    expect(screen.getByText(/only sink/i)).toBeTruthy()
  })
})
