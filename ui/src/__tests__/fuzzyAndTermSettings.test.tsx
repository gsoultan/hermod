import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { useState, type ComponentType } from 'react'
import { describe, expect, it } from 'vitest'
import { FuzzyLookupConfig } from '@/components/workflow/Transformation/configs/enrichment/FuzzyLookupConfig'
import { TermExtractionConfig } from '@/components/workflow/Transformation/configs/enrichment/TermExtractionConfig'

/**
 * Two editors whose settings did not reach their node.
 *
 * Fuzzy Lookup's Options box stores JSON text and the node read only a list,
 * so a node built here matched nothing. Term Extraction's "Min Word Length"
 * and "Stopwords" wrote keys the node never read. The node reads all of them
 * now (fuzzy_lookup_options_test.go, term_extraction_settings_test.go); these
 * are the editor's half: it shows what the node will read, in either shape a
 * config can arrive in, and says when the node will refuse it.
 */

type Config = Record<string, any>

function Harness({ Editor, initial }: { Editor: ComponentType<any>; initial: Config }) {
  const [config, setConfig] = useState<Config>(initial)
  return (
    <MantineProvider>
      <Editor
        config={config}
        updateNodeConfig={(_id: string, patch: Config) => setConfig((prev) => ({ ...prev, ...patch }))}
        nodeId="n1"
        fieldPaths={['city', 'note']}
      />
      <output data-testid="config">{JSON.stringify(config)}</output>
    </MantineProvider>
  )
}

const config = () => JSON.parse(screen.getByTestId('config').textContent || '{}')
const NOT_A_LIST = /must be a JSON list/i

describe('Fuzzy Lookup options', () => {
  const options = () => screen.getByRole('textbox', { name: /options/i }) as HTMLTextAreaElement

  // A node made through the API or a bundle holds a list, not text.
  it('shows a list as the JSON it is', () => {
    render(<Harness Editor={FuzzyLookupConfig} initial={{ field: 'city', options: ['Jakarta', 'Bandung'] }} />)
    expect(JSON.parse(options().value)).toEqual(['Jakarta', 'Bandung'])
  })

  it('shows text as it was typed', () => {
    render(<Harness Editor={FuzzyLookupConfig} initial={{ field: 'city', options: '["Jakarta"]' }} />)
    expect(options().value).toBe('["Jakarta"]')
    expect(screen.queryByText(NOT_A_LIST)).toBeNull()
  })

  // The node refuses these on every record; the editor says so first.
  it.each([
    ['words with commas', 'Jakarta, Bandung'],
    ['a JSON object', '{"a": "Jakarta"}'],
    ['a JSON string', '"Jakarta"'],
    ['unfinished JSON', '["Jakarta",'],
  ])('says %s are not a list', (_name, text) => {
    render(<Harness Editor={FuzzyLookupConfig} initial={{ field: 'city', options: text }} />)
    expect(screen.getByText(NOT_A_LIST)).toBeInTheDocument()
  })

  it.each([
    ['nothing yet', undefined],
    ['blank text', '  '],
    ['an empty list', '[]'],
  ])('says nothing about %s', (_name, text) => {
    render(<Harness Editor={FuzzyLookupConfig} initial={{ field: 'city', options: text }} />)
    expect(screen.queryByText(NOT_A_LIST)).toBeNull()
  })

  it('stores what is typed as text, which the node reads', async () => {
    const user = userEvent.setup()
    render(<Harness Editor={FuzzyLookupConfig} initial={{ field: 'city' }} />)
    await user.click(options())
    await user.paste('["Jakarta"]')
    expect(config().options).toBe('["Jakarta"]')
  })
})

describe('Term Extraction settings', () => {
  // "".split(",") is [""], which the input drew as one blank word.
  it('shows no stop words when none are set', () => {
    const { container } = render(<Harness Editor={TermExtractionConfig} initial={{ field: 'note', stopWords: '' }} />)
    expect(container.querySelectorAll('.mantine-Pill-root')).toHaveLength(0)
  })

  it('shows stop words stored as text or as a list', () => {
    for (const stopWords of ['urgent, old', ['urgent', 'old']]) {
      const { container, unmount } = render(<Harness Editor={TermExtractionConfig} initial={{ field: 'note', stopWords }} />)
      const pills = [...container.querySelectorAll('.mantine-Pill-label')].map((p) => p.textContent)
      expect(pills).toEqual(['urgent', 'old'])
      unmount()
    }
  })

  it('stores stop words as the text the node reads', async () => {
    const user = userEvent.setup()
    render(<Harness Editor={TermExtractionConfig} initial={{ field: 'note' }} />)
    await user.type(screen.getByPlaceholderText('Add words to ignore'), 'urgent{Enter}old{Enter}')
    expect(config().stopWords).toBe('urgent,old')
  })

  it('says its words are added to the built-in ones', () => {
    render(<Harness Editor={TermExtractionConfig} initial={{ field: 'note' }} />)
    expect(screen.getByText(/as well as the common words/i)).toBeInTheDocument()
  })

  // minLen is the key the node used to read, so a node made through the API
  // may hold it. The input showed 3 for such a node whatever it held.
  it('shows the word length the node will use', () => {
    const { unmount } = render(<Harness Editor={TermExtractionConfig} initial={{ field: 'note', minLen: 5 }} />)
    expect(screen.getByRole('textbox', { name: /min word length/i })).toHaveValue('5')
    unmount()
    render(<Harness Editor={TermExtractionConfig} initial={{ field: 'note', minLength: 6, minLen: 5 }} />)
    expect(screen.getByRole('textbox', { name: /min word length/i })).toHaveValue('6')
  })
})
