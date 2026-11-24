import { useState } from 'react'
import { render, screen, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { QueueSinkConfig } from '@/components/workflow/Sink/QueueSinkConfig'
import { missingConnectionFields } from '@/lib/connectorRequirements'

// The RabbitMQ sink publishes to a queue, or to an exchange of a type the user
// picks. Each type needs different fields, and a queue is optional once an
// exchange is used, so the form shows only what the chosen type needs and says
// what each choice does.

let latest: Record<string, string> = {}

function Harness({ initial }: { initial: Record<string, string> }) {
  const [config, setConfig] = useState<Record<string, string>>(initial)
  const updateConfig = (key: string, value: string) =>
    setConfig((c) => {
      const next = { ...c, [key]: value }
      latest = next
      return next
    })
  return <QueueSinkConfig type="rabbitmq_queue" config={config} updateConfig={updateConfig} />
}

const setup = (initial: Record<string, string> = { host: 'mq' }) => {
  latest = { ...initial }
  render(
    <MantineProvider>
      <Harness initial={initial} />
    </MantineProvider>,
  )
  return userEvent.setup()
}

async function chooseType(user: ReturnType<typeof userEvent.setup>, label: RegExp) {
  await user.click(screen.getByRole('radio', { name: label }))
}

describe('rabbitmq sink: queue or exchange', () => {
  it('opens a sink saved before exchanges on its queue, with no exchange fields', () => {
    setup({ host: 'mq', queue_name: 'orders' })
    expect((screen.getByLabelText(/^queue name/i) as HTMLInputElement).value).toBe('orders')
    expect(screen.queryByLabelText(/exchange name/i)).toBeNull()
    expect(screen.getByText(/straight to this queue/i)).toBeTruthy()
  })

  it('shows the exchange fields once the user chooses an exchange, starting as direct', async () => {
    const user = setup()
    await user.click(screen.getByRole('radio', { name: /an exchange/i }))
    expect(screen.getByLabelText(/exchange name/i)).toBeTruthy()
    expect(latest.exchange_type).toBe('direct')
    expect(screen.getByLabelText(/^routing key/i)).toBeTruthy()
  })

  it('asks a fanout exchange for no routing key and says why', async () => {
    const user = setup({ host: 'mq', exchange: 'events', exchange_type: 'direct' })
    await chooseType(user, /fanout/i)
    expect(latest.exchange_type).toBe('fanout')
    expect(screen.queryByLabelText(/^routing key/i)).toBeNull()
    expect(screen.getAllByText(/every queue bound/i).length).toBeGreaterThan(0)
  })

  it('explains topic wildcards where the binding key is entered', async () => {
    setup({ host: 'mq', exchange: 'events', exchange_type: 'topic', routing_key: 'orders.created', queue_name: 'q' })
    expect(screen.getByLabelText(/^routing key/i)).toBeTruthy()
    expect(screen.getByLabelText(/^binding key/i)).toBeTruthy()
    expect(screen.getAllByText(/# matches/i).length).toBeGreaterThan(0)
  })

  it('asks a headers exchange for headers and how to match them, not a routing key', () => {
    setup({ host: 'mq', exchange: 'events', exchange_type: 'headers' })
    expect(screen.queryByLabelText(/^routing key/i)).toBeNull()
    expect(screen.getByRole('textbox', { name: /^headers/i })).toBeTruthy()
    expect(screen.getByRole('combobox', { name: /match when/i })).toBeTruthy()
  })

  it('warns that an exchange with no queue bound drops messages', () => {
    setup({ host: 'mq', exchange: 'events', exchange_type: 'fanout' })
    expect(screen.getByText(/no queue is bound/i)).toBeTruthy()
  })

  it('does not warn once a queue is bound', () => {
    setup({ host: 'mq', exchange: 'events', exchange_type: 'fanout', queue_name: 'q' })
    expect(screen.queryByText(/no queue is bound/i)).toBeNull()
  })

  it('summarises where a record will go', () => {
    setup({ host: 'mq', exchange: 'events', exchange_type: 'topic', routing_key: 'orders.created', queue_name: 'q', binding_key: 'orders.#' })
    const summary = screen.getByTestId('rabbitmq-route-summary').textContent ?? ''
    expect(summary).toMatch(/topic exchange events/i)
    expect(summary).toMatch(/orders\.created/)
    expect(summary).toMatch(/queue q/i)
    expect(summary).toMatch(/orders\.#/)
  })

  it('clears every exchange setting when the user goes back to a queue', async () => {
    const user = setup({ host: 'mq', exchange: 'events', exchange_type: 'topic', routing_key: 'a.b', binding_key: 'a.*', queue_name: 'q' })
    await user.click(screen.getByRole('radio', { name: /a queue/i }))
    for (const key of ['exchange', 'exchange_type', 'routing_key', 'binding_key', 'headers', 'binding_headers', 'headers_match', 'exchange_declare']) {
      expect(latest[key] ?? '').toBe('')
    }
    expect(latest.queue_name).toBe('q')
  })

  it('creates the exchange unless told it already exists', () => {
    setup({ host: 'mq', exchange: 'events', exchange_type: 'fanout' })
    const box = screen.getByLabelText(/create the exchange/i) as HTMLInputElement
    expect(box.checked).toBe(true)
    fireEvent.click(box)
    expect(latest.exchange_declare).toBe('false')
  })
})

describe('rabbitmq sink: what the connection step requires', () => {
  const missing = (cfg: Record<string, string>) => missingConnectionFields('sink', 'rabbitmq_queue', cfg)

  it('needs a queue name when publishing to a queue', () => {
    expect(missing({ host: 'mq' })).toEqual(['Queue name'])
  })
  it('needs an exchange name, not a queue, once an exchange is chosen', () => {
    expect(missing({ host: 'mq', exchange_type: 'fanout' })).toEqual(['Exchange name'])
    expect(missing({ host: 'mq', exchange: 'events', exchange_type: 'fanout' })).toEqual([])
  })
  it('needs a routing key for direct and topic', () => {
    expect(missing({ host: 'mq', exchange: 'e', exchange_type: 'direct' })).toEqual(['Routing key'])
    expect(missing({ host: 'mq', exchange: 'e', exchange_type: 'topic' })).toEqual(['Routing key'])
  })
  it('needs headers for a headers exchange', () => {
    expect(missing({ host: 'mq', exchange: 'e', exchange_type: 'headers' })).toEqual(['Headers'])
  })
  it('needs a binding key when a bound queue follows a per-record routing key', () => {
    expect(missing({ host: 'mq', exchange: 'e', exchange_type: 'topic', routing_key: 'db.{{.table}}', queue_name: 'q' })).toEqual(['Binding key'])
    expect(missing({ host: 'mq', exchange: 'e', exchange_type: 'topic', routing_key: 'db.{{.table}}', queue_name: 'q', binding_key: 'db.*' })).toEqual([])
  })
})
