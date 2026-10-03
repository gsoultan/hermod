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
    expect(screen.getByLabelText(/service account key/i)).toBeInTheDocument()
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
    expect(screen.getByRole('radio', { name: /combination of topics/i })).toBeChecked()
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
 * The data section is where people got stuck: three choices named after the
 * implementation, then a second list of "values to add", and nothing saying
 * what data is for. It is now one question -- which fields does the app get --
 * answered by ticking the incoming row's columns.
 */
describe('the fcm app data section', () => {
  it('says what data is before asking anything about it', () => {
    ui()
    const section = screen.getByRole('group', { name: /app data/i })
    expect(within(section).getByText(/never shown to people/i)).toBeInTheDocument()
  })

  it('names each choice by what the app receives and writes the key the sink reads', () => {
    const written: Written = []
    ui({}, { written })

    // No data_mode is the sink's default, and the form must show it as such.
    expect(screen.getByRole('radio', { name: /whole row as json/i })).toBeChecked()
    expect(screen.getByRole('radio', { name: /all fields/i })).toBeInTheDocument()

    fireEvent.click(screen.getByRole('radio', { name: /selected fields/i }))
    expect(written).toContainEqual(['data_mode', 'none'])
  })

  it('adds a column of the incoming row in one click and takes it out again', () => {
    const written: Written = []
    ui(
      { data_mode: 'none' },
      {
        written,
        availableFields: [
          { path: 'order_no', type: 'string' },
          { path: 'after', type: 'object' },
          { path: 'after.order_no', type: 'string' },
        ],
      },
    )

    const chip = screen.getByRole('checkbox', { name: 'order_no' })
    // The change event's image is not a column; a chip for it would send the
    // whole row as one string.
    expect(screen.queryByRole('checkbox', { name: 'after' })).not.toBeInTheDocument()

    fireEvent.click(chip)
    const added = written.filter(([key]) => key === 'data_json').pop()
    expect(JSON.parse(added![1] as string)).toEqual({ order_no: '{{.order_no}}' })
    expect(screen.getByRole('checkbox', { name: 'order_no' })).toBeChecked()
    // The editor below shows the same entry, so the two never disagree.
    expect(screen.getByLabelText('Key 1 name')).toHaveValue('order_no')

    fireEvent.click(screen.getByRole('checkbox', { name: 'order_no' }))
    expect(written.filter(([key]) => key === 'data_json').pop()).toEqual(['data_json', ''])
  })

  it('does not offer the column the message is addressed by', () => {
    ui(
      { data_mode: 'none', device_token: '{{.after.fcm_token}}' },
      { availableFields: [{ path: 'order_no', type: 'string' }, { path: 'fcm_token', type: 'string' }] },
    )
    expect(screen.getByRole('checkbox', { name: 'order_no' })).toBeInTheDocument()
    expect(screen.queryByRole('checkbox', { name: 'fcm_token' })).not.toBeInTheDocument()
  })

  it('offers no column chips when every column is already sent', () => {
    ui({ data_mode: 'fields' }, { availableFields: [{ path: 'order_no', type: 'string' }] })
    expect(screen.queryByRole('checkbox', { name: 'order_no' })).not.toBeInTheDocument()
    expect(screen.getByText('Extra keys')).toBeInTheDocument()
  })

  it('takes a custom value as a row and stores the JSON object the sink parses', () => {
    const written: Written = []
    ui({ data_mode: 'none' }, { written })

    fireEvent.click(screen.getByRole('button', { name: /add key/i }))
    fireEvent.change(screen.getByLabelText('Key 1 name'), { target: { value: 'deeplink' } })
    fireEvent.change(screen.getByLabelText('Key 1 value'), { target: { value: 'app://orders/{{.id}}' } })

    const last = written.filter(([key]) => key === 'data_json').pop()
    expect(JSON.parse(last![1] as string)).toEqual({ deeplink: 'app://orders/{{.id}}' })
  })

  // The run's refusal points at this field by this name.
  it('puts the oversize choice beside the data it is about', () => {
    ui()
    const section = screen.getByRole('group', { name: /app data/i })
    expect(within(section).getByLabelText(/if the data does not fit/i)).toBeInTheDocument()
  })
})

describe('the fcm service account', () => {
  it('says when what was pasted is not the key file', () => {
    ui({ credentials_json: '{"type":"service_account"' })
    expect(screen.getByText(/not valid json/i)).toBeInTheDocument()
  })

  it('says when the JSON is not a service account key', () => {
    ui({ credentials_json: '{"project_id":"demo"}' })
    expect(screen.getByText(/no private_key/i)).toBeInTheDocument()
  })

  it('names the project a valid key sends to', () => {
    ui({ credentials_json: '{"type":"service_account","project_id":"demo-app","private_key":"k","client_email":"a@b"}' })
    expect(screen.getByText('demo-app')).toBeInTheDocument()
  })

  it('reads an uploaded key file into the field', async () => {
    const written: Written = []
    const { container } = ui({}, { written })
    const key = '{"type":"service_account","project_id":"from-file","private_key":"k","client_email":"a@b"}'
    const input = container.querySelector('input[type="file"]') as HTMLInputElement
    fireEvent.change(input, { target: { files: [new File([key], 'key.json', { type: 'application/json' })] } })

    await waitFor(() => expect(written).toContainEqual(['credentials_json', key]))
    expect(await screen.findByText('from-file')).toBeInTheDocument()
  })
})

/**
 * FCM refuses more than 4096 bytes of data, and a message is easier to get
 * right when it can be seen. The preview builds the message with the sink's own
 * code on the server, as the form changes, without a button to find first.
 */
describe('the live fcm message preview', () => {
  const sent = {
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
  }
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

  it('shows the message the incoming row becomes without being asked', async () => {
    let posted: any = null
    server.use(
      http.post('/api/sinks/fcm/preview', async ({ request }) => {
        posted = await request.json()
        return HttpResponse.json(sent)
      }),
    )

    ui(
      { credentials_json: '{"private_key":"secret"}', device_token: '{{.push_token}}' },
      { incomingPayload: { order_no: 'A-17', push_token: 'tok-1' } },
    )

    const preview = await screen.findByRole('region', { name: /message preview/i })
    expect(await within(preview).findByText('Order A-17')).toBeInTheDocument()
    expect(within(preview).getByText(/ready to send/i)).toBeInTheDocument()
    expect(within(preview).getByText(/2 devices/)).toBeInTheDocument()
    expect(within(preview).getByText('deeplink')).toBeInTheDocument()
    expect(within(preview).getByText('app://orders/A-17')).toBeInTheDocument()
    expect(within(preview).getByText(/25 of 4,096 bytes/)).toBeInTheDocument()
    expect(within(preview).getByText(/previous step sends here/i)).toBeInTheDocument()

    // The row the editor says reaches this sink, not an invented one.
    expect(posted.type).toBe('fcm')
    expect(posted.sample).toEqual({ order_no: 'A-17', push_token: 'tok-1' })
    // Rendering a message authenticates to nothing; the key stays in the form.
    expect(posted.config).not.toHaveProperty('credentials_json')
  })

  it('follows the form as it is edited', async () => {
    const titles: string[] = []
    server.use(
      http.post('/api/sinks/fcm/preview', async ({ request }) => {
        const body: any = await request.json()
        titles.push(body.config.title ?? '')
        return HttpResponse.json({ ...sent, message: { ...sent.message, notification: { title: body.config.title } } })
      }),
    )

    ui({ topic: 'orders', title: 'First' })
    expect(await screen.findByText('First', { selector: 'p' })).toBeInTheDocument()

    fireEvent.change(screen.getByLabelText(/^title/i), { target: { value: 'Second' } })
    expect(await screen.findByText('Second', { selector: 'p' })).toBeInTheDocument()
    expect(titles).toEqual(['First', 'Second'])
  })

  it('says a row is too big, how big, and what is taking the room', async () => {
    server.use(http.post('/api/sinks/fcm/preview', () => HttpResponse.json(refused)))
    ui({ topic: 'orders' }, { incomingPayload: { id: '7', notes: 'long' } })

    expect(await screen.findByText('Would fail')).toBeInTheDocument()
    expect(screen.getByText(/4,633 of 4,096 bytes/)).toBeInTheDocument()
    expect(screen.getByText(/payload \(4,580 bytes\)/)).toBeInTheDocument()
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

    expect(await screen.findByText(/shortened from 4,633 bytes/i)).toBeInTheDocument()
    expect(screen.getByText(/topic orders/i)).toBeInTheDocument()
  })

  it('says a message with no title or body shows nothing on screen', async () => {
    server.use(
      http.post('/api/sinks/fcm/preview', () =>
        HttpResponse.json({ ...sent, message: { topic: 'orders', data: { id: '7' } }, recipients: 0 }),
      ),
    )
    ui({ topic: 'orders' })
    expect(await screen.findByText(/silent push/i)).toBeInTheDocument()
  })

  it('shows a configuration the server cannot build', async () => {
    server.use(
      http.post('/api/sinks/fcm/preview', () =>
        HttpResponse.json({ error: 'fcm sink: the title template is not valid: unclosed action' }, { status: 400 }),
      ),
    )
    ui({ topic: 'orders', title: '{{.id' })
    expect(await screen.findByText(/unclosed action/)).toBeInTheDocument()
  })

  it('uses an example row when the editor has none, and says so', async () => {
    server.use(http.post('/api/sinks/fcm/preview', () => HttpResponse.json(sent)))
    ui({ topic: 'orders' })
    expect(await screen.findByText(/example row/i)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /sample row/i }))
    await waitFor(() =>
      expect((screen.getByRole('textbox', { name: /sample row/i }) as HTMLTextAreaElement).value).toContain('A-17'),
    )
  })

  // A form nobody has filled in yet is not a failure, and red says it is.
  it('asks for a recipient rather than calling an empty form a failure', async () => {
    server.use(
      http.post('/api/sinks/fcm/preview', () =>
        HttpResponse.json({
          ...refused,
          data_bytes: 0,
          largest: [],
          refused: 'fcm sink: no fcm destination for message 1042: the sink has no default token, topic or condition',
        }),
      ),
    )
    ui({})
    expect(await screen.findByText('Needs a recipient')).toBeInTheDocument()
    expect(screen.queryByText('Would fail')).not.toBeInTheDocument()
    // The package prefix is for logs, not for the person reading the form.
    expect(screen.getByText(/^no fcm destination for message 1042/)).toBeInTheDocument()
  })

  it('refuses a sample row that is not JSON without asking the server', async () => {
    let calls = 0
    server.use(
      http.post('/api/sinks/fcm/preview', () => {
        calls++
        return HttpResponse.json(sent)
      }),
    )
    ui({ topic: 'orders' }, { incomingPayload: { id: '7' } })
    await waitFor(() => expect(calls).toBe(1))

    fireEvent.click(screen.getByRole('button', { name: /sample row/i }))
    fireEvent.change(await screen.findByRole('textbox', { name: /sample row/i }), { target: { value: '{not json' } })

    expect(await screen.findByText(/not valid json/i)).toBeInTheDocument()
    expect(calls).toBe(1)
  })
})
