import { render, screen } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { VHostProvider } from '@/context/VHostContext'
import { server, signInAs } from '../test/setupTests'
import { http, HttpResponse } from 'msw'
import { TransformationForm } from '@/components/forms/TransformationForm'
import { vi } from 'vitest'

vi.mock('@tanstack/react-router', () => ({
  Link: (props: any) => <button {...props} />,
}))

function renderType(transType: string, extra: Record<string, unknown> = {}) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  render(
    <MantineProvider>
      <QueryClientProvider client={qc}>
        <VHostProvider>
          <TransformationForm
            selectedNode={{ id: 'n1', type: 'transformation', data: { transType, ...extra } } as any}
            updateNodeConfig={() => {}}
            availableFields={[]}
            incomingPayload={{ x: 1 }}
            sinkSchema={{}}
          />
        </VHostProvider>
      </QueryClientProvider>
    </MantineProvider>
  )
}

/**
 * Each transformation type configures itself exactly once.
 *
 * Configuration moved to a registry (configs/registry.ts) so that adding a
 * transformation never meant editing TransformationForm — but the inline
 * blocks the registry replaced were never deleted. Seven types therefore
 * rendered their settings twice: once under "Configuration" from the registry
 * and again under "Advanced" from the old code. A pipeline node showed two
 * "Steps" editors; a set node showed two field editors and two raw-JSON panes.
 * Edits in one pane appeared in the other on the next render, which is the
 * kind of thing that looks like a haunted form.
 */
describe('transformation config renders once per type', () => {
  beforeEach(() => {
    signInAs('editor')
    server.use(http.post('/api/transformations/test', () => HttpResponse.json({ ok: true })))
  })

  // Keyed on labels the registry components render — the survivors — so the
  // assertion is "exactly one" rather than "the inline one is gone". Where the
  // two versions used different labels (wasm: "Entrypoint Function" vs "WASM
  // Function Name") the pattern matches both, so it read 2 before and 1 after.
  const cases: Array<[string, RegExp, Record<string, unknown>?]> = [
    ['pipeline', /steps \(json array\)/i],
    ['lua', /lua script/i],
    ['wasm', /entrypoint function|wasm function name/i],
    ['foreach', /array path/i],
    ['aggregate', /field to aggregate/i],
    ['set', /fields \(json\)/i],
    ['advanced', /config \(json\)/i],
  ]

  // Count form controls only. A Mantine Select is labelled twice in the DOM —
  // its input and its (hidden) listbox div — so a raw getAllByLabelText reads 2
  // for a single control.
  const isControl = (el: HTMLElement) => el.tagName === 'INPUT' || el.tagName === 'TEXTAREA'

  for (const [type, label, extra] of cases) {
    it(`${type}: one "${label.source}" control`, async () => {
      renderType(type, extra)
      const labelled = await screen.findAllByLabelText(label, undefined, { timeout: 5000 })
      const controls = labelled.filter(isControl)
      expect(controls, `${type} renders its ${label.source} control ${controls.length} times`).toHaveLength(1)
    }, 20000)
  }

  // set and advanced both render SetFieldEditor; with one column configured it
  // shows one field row, whose path input is the thing to count. Two editors
  // meant two rows for the same column.
  for (const type of ['set', 'advanced']) {
    it(`${type}: one field editor`, async () => {
      renderType(type, { 'column.a': "'1'" })
      const rows = await screen.findAllByPlaceholderText('e.g. user.id', undefined, { timeout: 5000 })
      expect(rows, `${type} renders ${rows.length} field editors`).toHaveLength(1)
    }, 20000)
  }
})
