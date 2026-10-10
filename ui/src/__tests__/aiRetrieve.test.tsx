import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { ReactFlowProvider } from '@xyflow/react'
import { useState } from 'react'
import { describe, expect, it } from 'vitest'
import { AIRetrieveConfig } from '@/components/workflow/Transformation/configs/ai-retrieve/AIRetrieveConfig'
import { retrieveIssues } from '@/components/workflow/Transformation/configs/ai-retrieve/retrieveIssues'
import { resolveConfigComponent } from '@/components/workflow/Transformation/configs/registry'
import { TransformationNode } from '@/pages/workflows/WorkflowEditor/nodes/MiscNodes'
import { guideFor } from '@/lib/transformationGuide'

const messages = (cfg: Record<string, unknown>) => retrieveIssues(cfg).map((i) => `${i.severity}: ${i.message}`)

// retrieveNodeIssues in internal/workflow/transport/http/workflow_validation_ai.go,
// plus the provider check aiNodeIssues makes for every AI node.
describe('ai_retrieve validation, as the backend does it', () => {
  it('needs a provider before anything else', () => {
    expect(messages({ store: 'pgvector' })).toEqual(['error: Choose the AI provider that embeds the query.'])
  })

  it('needs a known store', () => {
    expect(messages({ provider: 'openai', query: 'q' })).toEqual([
      'error: Choose a vector store (pgvector or pinecone).',
    ])
    expect(messages({ provider: 'openai', query: 'q', store: 'milvus' })).toEqual([
      'error: Choose a vector store (pgvector or pinecone).',
    ])
  })

  it('reads the store name trimmed and in any case', () => {
    expect(messages({ provider: 'openai', query: 'q', store: ' PineCone ', indexHost: 'https://x' })).toEqual([])
  })

  it('needs a connection string and a table for pgvector', () => {
    expect(messages({ provider: 'openai', query: 'q', store: 'pgvector' })).toEqual([
      'error: pgvector needs a connection string.',
      'error: pgvector needs a table.',
    ])
    expect(messages({ provider: 'openai', query: 'q', store: 'pgvector', connectionString: ' ', table: 'docs' })).toEqual([
      'error: pgvector needs a connection string.',
    ])
  })

  it('needs the index host for pinecone', () => {
    expect(messages({ provider: 'openai', query: 'q', store: 'pinecone' })).toEqual([
      'error: Pinecone needs the index host.',
    ])
  })

  it('needs a query or a query field', () => {
    expect(messages({ provider: 'openai', store: 'pinecone', indexHost: 'h' })).toEqual([
      'error: Write a query or name the field that holds it.',
    ])
    expect(messages({ provider: 'openai', store: 'pinecone', indexHost: 'h', queryField: 'question' })).toEqual([])
  })

  it('warns about an API key typed into the node, as aiNodeIssues does', () => {
    expect(messages({ provider: 'openai', store: 'pinecone', indexHost: 'h', query: 'q', apiKey: 'sk-1' })).toEqual([
      'warning: The API key is stored in the workflow. Use a vhost secret instead.',
    ])
    expect(messages({ provider: 'openai', store: 'pinecone', indexHost: 'h', query: 'q', apiKey: '{{secret("K")}}' })).toEqual([])
  })
})

let saved: Record<string, any> = {}

function Harness({ initial }: { initial: Record<string, any> }) {
  const [config, setConfig] = useState(initial)
  return (
    <AIRetrieveConfig
      config={config}
      nodeId="r1"
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

describe('AI Retrieve settings', () => {
  it('is the editor for an ai_retrieve transformation', () => {
    expect(resolveConfigComponent('transformation', 'ai_retrieve')).toBe(AIRetrieveConfig)
  })

  it('shows the pgvector fields with the engine defaults as placeholders', () => {
    renderConfig({ provider: 'openai', store: 'pgvector' })
    expect(screen.getByRole('textbox', { name: /connection string/i })).toBeInTheDocument()
    expect(screen.getByRole('textbox', { name: /^table/i })).toBeInTheDocument()
    expect(screen.getByRole('textbox', { name: /vector column/i })).toHaveAttribute('placeholder', 'embedding')
    expect(screen.getByRole('textbox', { name: /id column/i })).toHaveAttribute('placeholder', 'id')
    expect(screen.getByRole('textbox', { name: /metadata column/i })).toHaveAttribute('placeholder', 'metadata')
    expect(screen.queryByRole('textbox', { name: /index host/i })).toBeNull()
    expect(screen.getByText('pgvector needs a connection string.')).toBeInTheDocument()
  })

  it('shows the Pinecone fields, with the store key under storeApiKey as a secret', () => {
    renderConfig({ provider: 'openai', store: 'pinecone' })
    fireEvent.change(screen.getByRole('textbox', { name: /index host/i }), {
      target: { value: 'https://docs-abc.svc.pinecone.io' },
    })
    expect(saved.indexHost).toBe('https://docs-abc.svc.pinecone.io')
    fireEvent.change(screen.getByRole('combobox', { name: /pinecone api key secret/i }), { target: { value: 'PINECONE_KEY' } })
    expect(saved.storeApiKey).toBe('{{secret("PINECONE_KEY")}}')
    expect(saved.apiKey).toBeUndefined()
    expect(screen.queryByRole('textbox', { name: /connection string/i })).toBeNull()
  })

  it('saves topK and minScore as text', () => {
    renderConfig({ provider: 'openai', store: 'pinecone', indexHost: 'h' })
    fireEvent.change(screen.getByRole('textbox', { name: /number of documents/i }), { target: { value: '8' } })
    expect(saved.topK).toBe('8')
    fireEvent.change(screen.getByRole('textbox', { name: /minimum score/i }), { target: { value: '0.75' } })
    expect(saved.minScore).toBe('0.75')
    expect(screen.getByRole('textbox', { name: /target field/i })).toHaveAttribute('placeholder', 'ai_context')
  })

  it('switches between a query template and a query field, keeping only one', async () => {
    const user = userEvent.setup()
    renderConfig({ provider: 'openai', store: 'pinecone', indexHost: 'h', query: '{{.q}}' })
    expect(screen.getByRole('textbox', { name: /query template/i })).toHaveValue('{{.q}}')
    await user.click(screen.getByText('A field'))
    expect(saved.query).toBe('')
    fireEvent.change(screen.getByRole('combobox', { name: /query field/i }), { target: { value: 'question' } })
    expect(saved.queryField).toBe('question')
  })

  it('warns when the provider cannot embed', () => {
    renderConfig({ provider: 'anthropic', store: 'pinecone', indexHost: 'h', query: 'q' })
    expect(screen.getByText(/cannot produce embeddings/i)).toBeInTheDocument()
  })

  it('offers only the metrics the engine knows', async () => {
    const user = userEvent.setup()
    renderConfig({ provider: 'openai', store: 'pgvector' })
    await user.click(screen.getByRole('combobox', { name: /distance metric/i }))
    for (const m of ['Cosine', 'Euclidean (L2)', 'Inner product']) {
      expect(screen.getByText(m)).toBeInTheDocument()
    }
    await user.click(screen.getByText('Euclidean (L2)'))
    expect(saved.metric).toBe('l2')
  })
})

describe('ai_retrieve on the canvas and in the guide', () => {
  it('is labelled AI Retrieve', () => {
    render(
      <MantineProvider>
        <ReactFlowProvider>
          <TransformationNode id="t1" data={{ label: 'Docs', transType: 'ai_retrieve' }} selected={false} />
        </ReactFlowProvider>
      </MantineProvider>,
    )
    expect(screen.getAllByText('AI Retrieve').length).toBeGreaterThan(0)
  })

  it('is described in plain words', () => {
    expect(guideFor('ai_retrieve').what).toMatch(/vector store/i)
  })
})
