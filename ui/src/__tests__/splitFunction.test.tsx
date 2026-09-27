/// <reference types="node" />
// node builtins are referenced here rather than added to tsconfig.app.json's
// `types`, for the reason matchesConditionParity.test.ts gives.

import { existsSync, readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { render, screen, within } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, it, expect, vi } from 'vitest'
import { VHostProvider } from '@/context/VHostContext'
import { TransformationForm } from '@/components/forms/TransformationForm'
import HelpContent from '@/components/workflow/Transformation/HelpContent'
import { server, signInAs } from '../test/setupTests'

vi.mock('@tanstack/react-router', () => ({
  Link: (props: any) => <button {...props} />,
}))

// split's contract is the fixture the engine's test runs
// (function_fixture_test.go). The library's example is one of its cases.
const FIXTURE_REL = 'pkg/infra/evaluator/testdata/functions/split.json'

function findFixture(): string {
  let dir = process.cwd()
  for (let i = 0; i < 6; i++) {
    const candidate = resolve(dir, FIXTURE_REL)
    if (existsSync(candidate)) return candidate
    dir = resolve(dir, '..')
  }
  throw new Error(`could not find ${FIXTURE_REL} walking up from ${process.cwd()}.`)
}

const fixture: { cases: { name: string; expr: string }[] } = JSON.parse(readFileSync(findFixture(), 'utf8'))

function renderFormulas() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  render(
    <MantineProvider>
      <QueryClientProvider client={qc}>
        <VHostProvider>
          <TransformationForm
            selectedNode={{ id: 'n1', type: 'transformation', data: { transType: 'advanced' } } as any}
            updateNodeConfig={() => {}}
            availableFields={[]}
            incomingPayload={{ full_name: 'Ada King Lovelace' }}
            sinkSchema={{}}
          />
        </VHostProvider>
      </QueryClientProvider>
    </MantineProvider>
  )
}

// A function nobody can find is a function nobody uses: formulas are written
// from the library, and the help modal is where the syntax is looked up.
describe('split in the editor', () => {
  beforeEach(() => {
    signInAs('editor')
    server.use(http.post('/api/transformations/test', () => HttpResponse.json({ ok: true })))
  })

  it('is offered by the function library, with an example the engine test runs', async () => {
    renderFormulas()

    const offered = await screen.findByRole('button', { name: /^insert split\(/i }, { timeout: 5000 })
    // The example is inserted as written, so it is a claim about the engine:
    // it is a fixture case, and the Go test runs it.
    const example = fixture.cases.find((c) => c.name === "part: the function library's example")
    expect(example, 'the fixture has lost the library example case').toBeDefined()
    expect(within(offered).getByText(example!.expr)).toBeInTheDocument()
  }, 20000)

  it('is listed in the help', () => {
    render(
      <MantineProvider>
        <HelpContent />
      </MantineProvider>
    )
    expect(screen.getByText(/^split\(/)).toBeInTheDocument()
  })
})
