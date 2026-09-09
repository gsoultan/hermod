import { render, screen } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { MessagingSourceConfig } from '@/components/workflow/Source/MessagingSourceConfig'
import { QueueSinkConfig } from '@/components/workflow/Sink/QueueSinkConfig'

const ui = (node: React.ReactNode) => render(<MantineProvider>{node}</MantineProvider>)
const noop = () => {}

/**
 * BuildConnectionString prefers url over the host fields, but the forms hid the
 * URL input as soon as a host was typed. A url entered first therefore kept
 * winning from behind a field nobody could see: edit the host, test again, and
 * the connection still goes wherever the hidden url points.
 */
describe('a connection URL that overrides the host fields stays visible', () => {
  it('keeps the source URL field on screen while it holds a value', () => {
    ui(<MessagingSourceConfig type="rabbitmq_queue" updateConfig={noop}
      config={{ host: 'localhost', port: '5672', url: 'amqp://elsewhere:5672/' }} />)
    expect(screen.getByDisplayValue('amqp://elsewhere:5672/')).toBeInTheDocument()
  })

  it('keeps the sink URL field on screen for both rabbitmq flavours', () => {
    for (const type of ['rabbitmq', 'rabbitmq_queue']) {
      const { unmount } = ui(<QueueSinkConfig type={type} updateConfig={noop}
        config={{ host: 'localhost', url: 'amqp://elsewhere:5672/' }} />)
      expect(screen.getByDisplayValue('amqp://elsewhere:5672/')).toBeInTheDocument()
      unmount()
    }
  })

  // The field was hidden to keep the common path uncluttered. That still holds
  // when there is nothing to disclose.
  it('still hides the empty URL field once a host is given', () => {
    ui(<MessagingSourceConfig type="rabbitmq_queue" updateConfig={noop}
      config={{ host: 'localhost', port: '5672' }} />)
    expect(screen.queryByLabelText(/RabbitMQ URL/i)).toBeNull()
  })
})
