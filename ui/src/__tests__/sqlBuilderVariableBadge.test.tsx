import { render, screen, within } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { describe, it, expect } from 'vitest'
import { SQLQueryBuilder } from '@/components/forms/SQLQueryBuilder'

// The DETECTED VARIABLES panel badged a token "Matched" purely on its presence
// in availableFields, which is built by recursing the editor's sample -- a
// different question from "will this bind a value". It said Matched for
// {{.after.payload}} while the pipeline bound NULL for it, so the one warning
// that could have caught the reported bug was pointing the wrong way.
//
// The badge now tracks the value the token resolves to.

function renderBuilder(query: string, props: Record<string, any> = {}) {
  return render(
    <MantineProvider>
      <SQLQueryBuilder
        type="source"
        sourceType="postgres"
        config={{}}
        initialQuery={query}
        availableFields={[{ path: 'payload', type: 'object' }]}
        sampleMessage={{
          operation: 'insert',
          payload: { registrantId: 'r-1' },
          after: { payload: { registrantId: 'r-1' } },
        }}
        {...props}
      />
    </MantineProvider>
  )
}

function badgeFor(token: string) {
  const label = screen.getByText(token)
  const row = label.closest('div')!.parentElement!
  return within(row).getByText(/Matched|Missing/)
}

describe('SQL builder detected variables', () => {
  it('badges an envelope path that resolves, even though the field list omits it', () => {
    renderBuilder('SELECT {{.after.payload}} FROM t')
    expect(badgeFor('{{.after.payload}}')).toHaveTextContent('Matched')
  })

  it('still badges a token nothing answers as Missing', () => {
    renderBuilder('SELECT {{.typoo}} FROM t')
    expect(badgeFor('{{.typoo}}')).toHaveTextContent('Missing')
  })

  it('badges a plain field that resolves', () => {
    renderBuilder('SELECT {{.payload}} FROM t')
    expect(badgeFor('{{.payload}}')).toHaveTextContent('Matched')
  })

  it('does not call a null value a match', () => {
    renderBuilder('SELECT {{.deleted_at}} FROM t', {
      sampleMessage: { deleted_at: null },
    })
    expect(badgeFor('{{.deleted_at}}')).toHaveTextContent('Missing')
  })
})
