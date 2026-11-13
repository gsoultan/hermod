import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { vi } from 'vitest'
import { QueueSinkConfig } from '@/components/workflow/Sink/QueueSinkConfig'

// The Redis Stream sink writes the keys pkg/infra/rediskv.ParseConn reads:
// addr, username, password, db and tls, plus stream. Values are text.
describe('Redis Stream sink form', () => {
  const renderSink = (config: any, updateConfig = vi.fn()) => {
    render(
      <MantineProvider>
        <QueueSinkConfig type="redis" config={config} updateConfig={updateConfig} />
      </MantineProvider>
    )
    return updateConfig
  }

  it('says what the address may be, and that localhost in a container is the container', () => {
    renderSink({})
    expect(screen.getByText(/redis:\/\/ or rediss:\/\//i)).toBeInTheDocument()
    expect(screen.getByText(/docker/i)).toBeInTheDocument()
  })

  it('writes the username, database and TLS switch as text', async () => {
    const update = renderSink({})
    await userEvent.type(screen.getByLabelText(/^username/i), 'u')
    expect(update).toHaveBeenLastCalledWith('username', 'u')
    await userEvent.type(screen.getByLabelText(/^database/i), '2')
    expect(update).toHaveBeenLastCalledWith('db', '2')
    await userEvent.click(screen.getByLabelText(/use tls/i))
    expect(update).toHaveBeenLastCalledWith('tls', 'true')
  })

  it('hides the password', () => {
    renderSink({})
    expect((screen.getByLabelText(/^password/i) as HTMLInputElement).type).toBe('password')
  })
})
