import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { useState } from 'react'
import { describe, expect, it } from 'vitest'
import { MimirAskConfig } from '@/components/workflow/Transformation/configs/ai-retrieve/MimirAskConfig'
import { resolveConfigComponent } from '@/components/workflow/Transformation/configs/registry'
import { guideFor } from '@/lib/transformationGuide'

/**
 * The Ask Documents node: the server, its key secret and the collection,
 * then the question as a template or a field, and the result settings that
 * change with the mode. Keys match pkg/comm/transformer/genai/mimirask.
 */

let saved: Record<string, any> = {}

function Harness({ initial }: { initial: Record<string, any> }) {
  const [config, setConfig] = useState(initial)
  return (
    <MimirAskConfig
      config={config}
      nodeId="a1"
      availableFields={['ticket.body', 'customer']}
      updateNodeConfig={(_id: string, patch: any) => setConfig((c: any) => (saved = { ...c, ...patch }))}
    />
  )
}

const renderConfig = (initial: Record<string, any>) => {
  saved = initial
  return render(
    <MantineProvider>
      <Harness initial={initial} />
    </MantineProvider>,
  )
}

describe('Ask Documents settings', () => {
  it('is the editor for a mimir_ask transformation, with a guide entry', () => {
    expect(resolveConfigComponent('transformation', 'mimir_ask')).toBe(MimirAskConfig)
    expect(guideFor('mimir_ask')?.title).toBe('Ask documents')
  })

  it('saves the server, the key as a secret reference and the collection', () => {
    renderConfig({})
    fireEvent.change(screen.getByRole('textbox', { name: /server address/i }), { target: { value: 'http://mimir:8080' } })
    expect(saved.url).toBe('http://mimir:8080')
    fireEvent.change(screen.getByRole('combobox', { name: /api key secret/i }), { target: { value: 'MIMIR_API_KEY' } })
    expect(saved.apiKey).toBe('{{secret.MIMIR_API_KEY}}')
    fireEvent.change(screen.getByRole('textbox', { name: /^collection/i }), { target: { value: 'col-1' } })
    expect(saved.collection).toBe('col-1')
  })

  it('asks the question from a template or a field, keeping only one', async () => {
    const user = userEvent.setup()
    renderConfig({ url: 'u', apiKey: '{{secret.K}}', collection: 'c', question: 'Can {{.customer}} return it?' })
    expect(screen.getByRole('textbox', { name: /question template/i })).toHaveValue('Can {{.customer}} return it?')
    await user.click(screen.getByText('A field'))
    expect(saved.question).toBe('')
    fireEvent.change(screen.getByRole('combobox', { name: /question field/i }), { target: { value: 'ticket.body' } })
    expect(saved.questionField).toBe('ticket.body')
  })

  it('offers sources and answer length for an answer, and a passage count for a search, saved as text', async () => {
    const user = userEvent.setup()
    renderConfig({ url: 'u', apiKey: '{{secret.K}}', collection: 'c', question: 'q' })
    fireEvent.change(screen.getByRole('textbox', { name: /^sources/i }), { target: { value: '2' } })
    expect(saved.sources).toBe('2')
    fireEvent.change(screen.getByRole('textbox', { name: /longest answer/i }), { target: { value: '300' } })
    expect(saved.maxTokens).toBe('300')
    expect(screen.getByRole('textbox', { name: /target field/i })).toHaveAttribute('placeholder', 'answer')

    await user.click(screen.getByRole('radio', { name: 'Passages only' }))
    expect(saved.mode).toBe('search')
    expect(screen.queryByRole('textbox', { name: /^sources/i })).toBeNull()
    fireEvent.change(screen.getByRole('textbox', { name: /^passages/i }), { target: { value: '10' } })
    expect(saved.limit).toBe('10')
    expect(screen.getByText(/Receives \{sources:/)).toBeInTheDocument()
  })
})
