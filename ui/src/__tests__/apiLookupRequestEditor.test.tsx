import { render, screen, within, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { useState } from 'react'
import { vi } from 'vitest'
import { APILookupConfig } from '@/components/workflow/Transformation/configs/enrichment/APILookupConfig'

// The api_lookup form put headers, query params and the body into three small
// raw-JSON boxes on a tab of their own, in a column a third of the drawer wide,
// with Test API Call on a different tab. Writing a session request meant
// hand-typing JSON into a four-row box and switching tabs to try it. The request
// is edited the way an HTTP client edits one now, and it is stored exactly as
// before: headers and queryParams as a JSON object string, body as text.

const FIELDS = ['after.user_id', 'after.entity_id', 'after.entity_type']

/** Renders the form against a real config object, the way the store holds it. */
function Harness({ initial, testLookup = () => {}, lastTest }: { initial: any; testLookup?: () => void; lastTest?: any }) {
  const [config, setConfig] = useState(initial)
  // env="test": no transitions, so a dropdown that opens is visible to role queries.
  return (
    <MantineProvider env="test">
      <APILookupConfig
        config={config}
        nodeId="n1"
        updateNodeConfig={(_id: string, patch: any) => {
          onPatch(patch)
          setConfig((c: any) => ({ ...c, ...patch }))
        }}
        fieldPaths={FIELDS}
        testLookup={testLookup}
        testing={false}
        lastTest={lastTest}
      />
      <pre data-testid="stored">{JSON.stringify(config)}</pre>
    </MantineProvider>
  )
}

let onPatch: (patch: any) => void = () => {}
const stored = () => JSON.parse(screen.getByTestId('stored').textContent || '{}')

beforeEach(() => {
  onPatch = () => {}
})

describe('api_lookup request editor', () => {
  it('edits headers as rows and stores them as the same JSON object', async () => {
    const user = userEvent.setup()
    render(<Harness initial={{ method: 'POST', url: 'https://x', headers: '{"Content-Type": "application/json"}' }} />)

    await user.click(screen.getByRole('tab', { name: /^headers/i }))
    // A header name suggests the common ones, so it is a combobox.
    expect(screen.getByRole('combobox', { name: 'Header 1 name' })).toHaveValue('Content-Type')
    expect(screen.getByRole('textbox', { name: 'Header 1 value' })).toHaveValue('application/json')

    await user.click(screen.getByRole('button', { name: /add header/i }))
    await user.type(screen.getByRole('combobox', { name: 'Header 2 name' }), 'X-Api-Key')
    await user.type(screen.getByRole('textbox', { name: 'Header 2 value' }), 'k1')

    expect(JSON.parse(stored().headers)).toEqual({ 'Content-Type': 'application/json', 'X-Api-Key': 'k1' })

    await user.click(screen.getByRole('button', { name: 'Remove header 1' }))
    expect(JSON.parse(stored().headers)).toEqual({ 'X-Api-Key': 'k1' })
  })

  it('opens headers it cannot show as rows as JSON, untouched', async () => {
    const user = userEvent.setup()
    const headers = '{"X-Filter": {"status": "open"}}'
    render(<Harness initial={{ method: 'POST', url: 'https://x', headers }} />)

    await user.click(screen.getByRole('tab', { name: /^headers/i }))
    expect(screen.getByRole('textbox', { name: /headers \(json\)/i })).toHaveValue(headers)
    expect(screen.getByText(/can.t be shown as rows/i)).toBeInTheDocument()
    expect(stored().headers).toBe(headers)
  })

  it('does not apply half-typed JSON, and applies it once it parses', async () => {
    render(<Harness initial={{ method: 'GET', url: 'https://x', queryParams: '' }} />)
    fireEvent.click(screen.getByRole('tab', { name: /^params/i }))
    fireEvent.click(screen.getByRole('radio', { name: 'JSON' }))

    const box = screen.getByRole('textbox', { name: /query params \(json\)/i })
    fireEvent.change(box, { target: { value: '{"page": ' } })
    expect(screen.getByText(/not valid json yet/i)).toBeInTheDocument()
    expect(stored().queryParams).toBe('')

    fireEvent.change(box, { target: { value: '{"page": "2"}' } })
    expect(stored().queryParams).toBe('{"page": "2"}')
  })

  it('counts what each tab holds', () => {
    render(
      <Harness
        initial={{
          method: 'POST',
          url: 'https://x',
          queryParams: '{"a": "1", "b": "{{.after.entity_id}}"}',
          headers: '{"Content-Type": "application/json"}',
          body: '{"id": "{{.after.user_id}}"}',
        }}
      />
    )
    expect(screen.getByRole('tab', { name: /^params\s*2$/i })).toBeInTheDocument()
    expect(screen.getByRole('tab', { name: /^headers\s*1$/i })).toBeInTheDocument()
  })

  it('keeps Test API Call on screen whichever tab is open', async () => {
    const user = userEvent.setup()
    const testLookup = vi.fn()
    render(<Harness initial={{ method: 'POST', url: 'https://x', body: '{}' }} testLookup={testLookup} />)

    for (const tab of [/^params/i, /^headers/i, /^body/i, /^auth/i, /^settings/i]) {
      await user.click(screen.getByRole('tab', { name: tab }))
      await user.click(screen.getByRole('button', { name: /test api call/i }))
    }
    expect(testLookup).toHaveBeenCalledTimes(5)
  })

  it('opens on the body for a method that sends one', () => {
    render(<Harness initial={{ method: 'POST', url: 'https://x', body: '{"a": 1}' }} />)
    expect(screen.getByRole('tab', { name: /^body/i })).toHaveAttribute('aria-selected', 'true')
    expect(screen.getByRole('textbox', { name: /request body/i })).toHaveValue('{"a": 1}')
  })
})

describe('api_lookup request body', () => {
  const openBody = async (body: string) => {
    const user = userEvent.setup()
    render(<Harness initial={{ method: 'POST', url: 'https://x', body }} />)
    await user.click(screen.getByRole('tab', { name: /^body/i }))
    return { user, box: screen.getByRole('textbox', { name: /request body/i }) as HTMLTextAreaElement }
  }

  const insert = async (user: ReturnType<typeof userEvent.setup>, path: string) => {
    await user.click(screen.getByRole('button', { name: /insert field into body/i }))
    await user.click(await screen.findByRole('option', { name: path }))
  }

  // Inside a JSON string the token goes in bare; outside one it goes in quoted,
  // because an unquoted token is not JSON until it is filled in.
  it('inserts a field token at the cursor, quoted only when it needs to be', async () => {
    const { user, box } = await openBody('{"created_by_id": ""}')
    box.focus()
    box.setSelectionRange(19, 19) // between the two quotes
    fireEvent.select(box)
    await insert(user, 'after.user_id')
    expect(stored().body).toBe('{"created_by_id": "{{.after.user_id}}"}')
  })

  it('quotes the token when the cursor is not inside a string', async () => {
    const { user, box } = await openBody('{"entity_id": }')
    box.focus()
    box.setSelectionRange(14, 14)
    fireEvent.select(box)
    await insert(user, 'after.entity_id')
    expect(stored().body).toBe('{"entity_id": "{{.after.entity_id}}"}')
  })

  it('formats valid JSON without touching its numbers', async () => {
    const { user } = await openBody('{"duration":604800000000000,"id":"{{.after.user_id}}"}')
    await user.click(screen.getByRole('button', { name: /^format$/i }))
    expect(stored().body).toBe('{\n  "duration": 604800000000000,\n  "id": "{{.after.user_id}}"\n}')
  })

  // Re-indenting through JSON.parse would round a 64-bit id to the nearest
  // double; the backend copies number literals byte for byte for that reason.
  it('formats without re-encoding a number too large for a double', async () => {
    const { user } = await openBody('{"id":9007199254740993,"ratio":1.50,"exp":1e3}')
    await user.click(screen.getByRole('button', { name: /^format$/i }))
    expect(stored().body).toBe('{\n  "id": 9007199254740993,\n  "ratio": 1.50,\n  "exp": 1e3\n}')
  })

  it('does not offer to format a body that is not JSON yet', async () => {
    await openBody('{"profile": {{.after.profile}}}')
    expect(screen.getByRole('button', { name: /^format$/i })).toBeDisabled()
  })

  it('edits the same body in a larger editor', async () => {
    const { user } = await openBody('{"a": 1}')
    await user.click(screen.getByRole('button', { name: /expand body editor/i }))

    const dialog = await screen.findByRole('dialog', { name: /request body/i })
    const big = within(dialog).getByRole('textbox', { name: /request body/i })
    expect(big).toHaveValue('{"a": 1}')
    fireEvent.change(big, { target: { value: '{"a": 2}' } })
    expect(stored().body).toBe('{"a": 2}')
  })

  it('says a body on a GET is still sent', async () => {
    render(<Harness initial={{ method: 'GET', url: 'https://x', body: '{"a": 1}' }} />)
    fireEvent.click(screen.getByRole('tab', { name: /^body/i }))
    expect(screen.getByText(/is still sent with this get/i)).toBeInTheDocument()
  })
})

describe('api_lookup test result', () => {
  // A toast disappears in a few seconds, and the reason a request was refused
  // -- which tokens went out empty -- is the longest text the form produces.
  it('keeps the last result on screen, error text in full', () => {
    const message =
      'api lookup failed: api lookup returned status 400: {"code":"invalid_argument","message":"invalid request body"}; ' +
      'these tokens had no value and were sent empty: {{.after.user_id}}, {{.after.entity_id}}'
    render(<Harness initial={{ method: 'POST', url: 'https://x' }} lastTest={{ ok: false, message }} />)

    const result = screen.getByTestId('api-lookup-test-result')
    expect(result).toHaveTextContent(/last test failed/i)
    expect(result).toHaveTextContent('{{.after.user_id}}, {{.after.entity_id}}')
  })
})
