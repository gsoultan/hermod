import { useState } from 'react'
import { render, screen, waitFor, fireEvent, within } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { describe, it, expect } from 'vitest'
import { http, HttpResponse } from 'msw'
import { server } from '../test/setupTests'
import { FcmSinkConfig } from '@/components/workflow/Sink/FcmSinkConfig'

type Written = Array<[string, unknown]>

/**
 * The form is controlled by its parent, so a test that wants to see what a
 * click *does* needs a parent that applies the write — which is the half the
 * destination picker was missing.
 */
function Harness({
  initial,
  written,
  ...rest
}: {
  initial: Record<string, string>
  written?: Written
  availableFields?: Array<{ path: string; type: string }>
  incomingPayload?: unknown
}) {
  const [config, setConfig] = useState<Record<string, any>>(initial)
  return (
    <FcmSinkConfig
      config={config}
      updateConfig={(key, value) => {
        written?.push([key, value])
        setConfig((current) => ({ ...current, [key]: value }))
      }}
      {...rest}
    />
  )
}

const ui = (initial: Record<string, string> = {}, rest: Omit<Parameters<typeof Harness>[0], 'initial'> = {}) =>
  render(
    <MantineProvider>
      <Harness initial={initial} {...rest} />
    </MantineProvider>,
  )

/**
 * Forty-five fields used to arrive in one scroll, with a notification channel
 * and an APNs thread id given the same weight as the destination. Nothing was
 * wrong with any one of them; the form just never said which three mattered.
 */
describe('the fcm form shows what is needed first', () => {
  it('keeps the per-platform options folded away until they are asked for', async () => {
    ui()
    expect(screen.getByLabelText(/service account json/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/^title/i)).toBeInTheDocument()
    expect(screen.queryByLabelText(/notification channel/i)).not.toBeInTheDocument()
    expect(screen.queryByLabelText(/collapse id/i)).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /android options/i }))
    expect(await screen.findByLabelText(/notification channel/i)).toBeInTheDocument()
  })

  // Folding a section must not hide that something in it is set: a sink that
  // sends at high priority should say so without being opened.
  it('opens a section that already has settings and counts them', () => {
    ui({ android_priority: 'high', android_ttl: '10m' })
    expect(screen.getByRole('button', { name: /android options.*2 set/i })).toBeInTheDocument()
    expect(screen.getByLabelText(/notification channel/i)).toBeInTheDocument()
    expect(screen.queryByLabelText(/collapse id/i)).not.toBeInTheDocument()
  })

  it('asks only for devices and a topic when the action moves devices', () => {
    ui({ action: 'subscribe' })
    expect(screen.getByLabelText(/device tokens/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/^topic/i)).toBeInTheDocument()
    expect(screen.queryByLabelText(/^title/i)).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /check with a sample row/i })).not.toBeInTheDocument()
  })
})

/**
 * The destination was read back out of the config: whichever of the three keys
 * held a value. Picking "Topic" cleared the other two, found all three empty,
 * and fell back to "Device token" — so the picker snapped back and the topic
 * field never appeared. A topic or a condition could not be configured at all.
 */
describe('the fcm destination picker', () => {
  it('shows the topic field when a topic is chosen', () => {
    ui()
    expect(screen.getByLabelText(/device token/i)).toBeInTheDocument()

    fireEvent.click(screen.getByRole('radio', { name: /a topic/i }))

    expect(screen.getByLabelText(/topic name/i)).toBeInTheDocument()
    expect(screen.queryByLabelText(/device token/i)).not.toBeInTheDocument()
  })

  it('opens on the destination a saved sink already has', () => {
    ui({ condition: "'orders' in topics" })
    expect(screen.getByRole('radio', { name: /a condition/i })).toBeChecked()
    expect(screen.getByLabelText(/^condition/i)).toHaveValue("'orders' in topics")
  })

  // FCM accepts exactly one destination, and fcm.New refuses a sink with two.
  it('clears the destination it is switched away from', () => {
    const written: Written = []
    ui({ device_token: '{{.push_token}}' }, { written })

    fireEvent.click(screen.getByRole('radio', { name: /a topic/i }))

    expect(written).toContainEqual(['device_token', ''])
  })

  // The subscribe actions need a device list *and* a topic. Switched back to
  // sending, both were still set, one of them in a field no longer on screen,
  // and fcm.New refused the sink for having two destinations.
  it('keeps one destination when the action goes back to sending', async () => {
    const written: Written = []
    ui({ action: 'subscribe', device_token: '{{.push_token}}', topic: 'orders' }, { written })

    fireEvent.click(screen.getByRole('combobox', { name: /what each message does/i }))
    fireEvent.click(await screen.findByRole('option', { name: 'Send a message' }))

    expect(written).toContainEqual(['action', 'send'])
    expect(written).toContainEqual(['device_token', ''])
    expect(screen.getByLabelText(/topic name/i)).toHaveValue('orders')
  })

  it('inserts a column as a template rather than making the operator type one', async () => {
    const written: Written = []
    ui({}, { written, availableFields: [{ path: 'push_token', type: 'string' }] })

    fireEvent.click(screen.getByRole('button', { name: /insert field into device token/i }))
    fireEvent.click(await screen.findByRole('option', { name: 'push_token' }))

    expect(written).toContainEqual(['device_token', '{{.push_token}}'])
  })
})

/**
 * "Envelope", "Fields" and "None" named the implementation. What an operator
 * is deciding is how much of the row goes to the handset, and the default —
 * all of it, as one string — is the choice that does not fit.
 */
describe('the fcm data choices', () => {
  it('describes each choice by what the app receives and writes the key the sink reads', () => {
    const written: Written = []
    ui({}, { written })

    const whole = screen.getByRole('radio', { name: /the whole row as one json text/i })
    // No data_mode is the sink's default, and the form must show it as such.
    expect(whole).toBeChecked()
    expect(screen.getByRole('radio', { name: /every column of the row/i })).toBeInTheDocument()

    fireEvent.click(screen.getByRole('radio', { name: /only the values listed below/i }))
    expect(written).toContainEqual(['data_mode', 'none'])
  })

  it('takes a value as a row and stores the JSON object the sink parses', () => {
    const written: Written = []
    ui({ data_mode: 'none' }, { written })

    fireEvent.click(screen.getByRole('button', { name: /add value/i }))
    fireEvent.change(screen.getByLabelText('Value 1 name'), { target: { value: 'deeplink' } })
    fireEvent.change(screen.getByLabelText('Value 1 value'), { target: { value: 'app://orders/{{.id}}' } })

    const last = written.filter(([key]) => key === 'data_json').pop()
    expect(last).toBeDefined()
    expect(JSON.parse(last![1] as string)).toEqual({ deeplink: 'app://orders/{{.id}}' })
  })

  // The run's refusal points at this field by this name.
  it('puts the oversize choice beside the data it is about', () => {
    const written: Written = []
    ui({}, { written })
    const section = screen.getByRole('group', { name: /data sent to the app/i })
    expect(within(section).getByLabelText(/if the data does not fit/i)).toBeInTheDocument()
  })
})

/**
 * FCM refuses more than 4096 bytes of data and the only thing that said a row
 * was too wide was the run that dead-lettered it. The check asks the server,
 * which builds the message with the sink's own code.
 */
describe('checking the fcm message against a sample row', () => {
  const refused = {
    recipients: 0,
    data_bytes: 4633,
    sent_data_bytes: 0,
    limit: 4096,
    largest: [
      { key: 'payload', bytes: 4580 },
      { key: 'operation', bytes: 15 },
    ],
    refused: 'fcm sink: the data for message preview is 4633 bytes and FCM accepts at most 4096.',
    sample: { id: '7' },
  }

  it('says a row is too big, how big, and what is taking the room', async () => {
    let posted: any = null
    server.use(
      http.post('/api/sinks/fcm/preview', async ({ request }) => {
        posted = await request.json()
        return HttpResponse.json(refused)
      }),
    )

    ui(
      { credentials_json: '{"private_key":"secret"}', topic: 'orders' },
      { incomingPayload: { id: '7', notes: 'long' } },
    )
    fireEvent.click(screen.getByRole('button', { name: /check with a sample row/i }))

    expect(await screen.findByText(/4,633 of 4,096 bytes/)).toBeInTheDocument()
    expect(screen.getByText(/this row would fail/i)).toBeInTheDocument()
    expect(screen.getByText(/payload \(4,580 bytes\)/)).toBeInTheDocument()

    // The row the editor says reaches this sink, not an invented one.
    expect(posted.type).toBe('fcm')
    expect(posted.sample).toEqual({ id: '7', notes: 'long' })
    expect(posted.config.topic).toBe('orders')
    // Rendering a message authenticates to nothing; the key stays in the form.
    expect(posted.config).not.toHaveProperty('credentials_json')
  })

  it('shows the message a row that fits would send', async () => {
    server.use(
      http.post('/api/sinks/fcm/preview', () =>
        HttpResponse.json({
          message: {
            token: 'tok-1',
            notification: { title: 'Order A-17', body: 'Ana' },
            data: { deeplink: 'app://orders/A-17' },
          },
          recipients: 2,
          data_bytes: 25,
          sent_data_bytes: 25,
          limit: 4096,
          largest: [{ key: 'deeplink', bytes: 25 }],
          sample: { order_no: 'A-17' },
        }),
      ),
    )

    ui({ device_token: '{{.push_token}}' })
    fireEvent.click(screen.getByRole('button', { name: /check with a sample row/i }))

    expect(await screen.findByText(/this row would be sent/i)).toBeInTheDocument()
    expect(screen.getByText(/25 of 4,096 bytes/)).toBeInTheDocument()
    expect(screen.getByText(/2 devices/)).toBeInTheDocument()
    // Exact: the wire JSON below it holds the same title inside a longer text.
    expect(screen.getByText('Order A-17')).toBeInTheDocument()
    // With no row from the editor the server says what it used, and it is editable.
    await waitFor(() =>
      expect((screen.getByRole('textbox', { name: /sample row/i }) as HTMLTextAreaElement).value).toContain('A-17'),
    )
  })

  it('says when the data was shortened to fit rather than sent whole', async () => {
    server.use(
      http.post('/api/sinks/fcm/preview', () =>
        HttpResponse.json({
          message: { topic: 'orders', data: { payload: 'x…[truncated]' } },
          recipients: 0,
          data_bytes: 4633,
          sent_data_bytes: 4096,
          limit: 4096,
          sample: { id: '7' },
        }),
      ),
    )

    ui({ topic: 'orders', on_oversize: 'truncate' })
    fireEvent.click(screen.getByRole('button', { name: /check with a sample row/i }))

    expect(await screen.findByText(/this row would be sent/i)).toBeInTheDocument()
    expect(screen.getByText(/shortened from 4,633 bytes/i)).toBeInTheDocument()
  })

  it('shows a configuration the server cannot build beside the button', async () => {
    server.use(
      http.post('/api/sinks/fcm/preview', () =>
        HttpResponse.json({ error: 'fcm sink: the title template is not valid: unclosed action' }, { status: 400 }),
      ),
    )

    ui({ topic: 'orders', title: '{{.id' })
    fireEvent.click(screen.getByRole('button', { name: /check with a sample row/i }))

    expect(await screen.findByText(/unclosed action/)).toBeInTheDocument()
  })

  it('refuses a sample row that is not JSON without asking the server', async () => {
    let calls = 0
    server.use(
      http.post('/api/sinks/fcm/preview', () => {
        calls++
        return HttpResponse.json({ ...refused, refused: undefined, message: { topic: 'orders' } })
      }),
    )

    ui({ topic: 'orders' })
    fireEvent.click(screen.getByRole('button', { name: /check with a sample row/i }))
    const sample = await screen.findByRole('textbox', { name: /sample row/i })
    await waitFor(() => expect(calls).toBe(1))

    fireEvent.change(sample, { target: { value: '{not json' } })
    fireEvent.click(screen.getByRole('button', { name: /check again/i }))

    expect(await screen.findByText(/not valid json/i)).toBeInTheDocument()
    expect(calls).toBe(1)
  })
})
