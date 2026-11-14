import { afterEach, describe, expect, it, vi } from 'vitest'
import { render } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { OtherSourceConfig } from '@/components/workflow/Source/OtherSourceConfig'

// A new source starts with an empty config, so these forms rendered their
// inputs with value={undefined} -- uncontrolled -- and the first keystroke
// made them controlled. React logs that as an error on every new webhook,
// form, gRPC, cron, HTTP, GraphQL and WebSocket source.
const TYPES = ['webhook', 'form', 'grpc', 'cron', 'http', 'graphql', 'websocket', 'firebase', 'googlesheets', 'googleanalytics']

describe('OtherSourceConfig on a new source', () => {
  afterEach(() => vi.restoreAllMocks())

  for (const type of TYPES) {
    it(`${type}: inputs stay controlled once a value is typed`, () => {
      const errors = vi.spyOn(console, 'error').mockImplementation(() => {})
      const ui = (config: Record<string, any>) => (
        <MantineProvider>
          <OtherSourceConfig config={config} updateConfig={() => {}} sourceType={type} />
        </MantineProvider>
      )
      const { rerender, container } = render(ui({}))
      // Every text field gets a value, as typing into each would.
      const filled: Record<string, any> = {}
      for (const k of ['path', 'schedule', 'payload', 'url', 'query', 'method', 'poll_interval', 'headers', 'data_path', 'spreadsheet_id', 'range', 'property_id', 'project_id', 'collection', 'credentials_json', 'api_key']) filled[k] = 'x'
      rerender(ui(filled))
      expect(container.querySelector('input, textarea')).not.toBeNull()
      const uncontrolled = errors.mock.calls.filter((c) => String(c[0]).includes('uncontrolled input to be controlled'))
      expect(uncontrolled).toEqual([])
    })
  }
})
