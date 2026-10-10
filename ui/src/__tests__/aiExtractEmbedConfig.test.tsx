import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { useState, type ComponentType } from 'react'
import { describe, it, expect } from 'vitest'
import { AIExtractConfig } from '@/components/workflow/Transformation/configs/ai/AIExtractConfig'
import { AIEmbedConfig } from '@/components/workflow/Transformation/configs/ai/AIEmbedConfig'

let saved: Record<string, any> = {}

function Harness({ Form, initial }: { Form: ComponentType<any>; initial: Record<string, any> }) {
  const [config, setConfig] = useState(initial)
  return (
    <Form
      config={config}
      nodeId="n1"
      updateNodeConfig={(_id: string, patch: any) => setConfig((c: any) => (saved = { ...c, ...patch }))}
    />
  )
}

const renderForm = (Form: ComponentType<any>, initial: Record<string, any>) => {
  saved = initial
  return render(
    <MantineProvider>
      <Harness Form={Form} initial={initial} />
    </MantineProvider>,
  )
}

const schemaBox = () => screen.getByRole('textbox', { name: /json schema/i })

// extract.go loadSchema: the schema is required, and it must parse as JSON
// before it can be compiled, or every message fails at the node.
describe('AI Extract settings', () => {
  it('requires a schema and shows an example of one', () => {
    renderForm(AIExtractConfig, { provider: 'anthropic' })
    expect(schemaBox()).toBeRequired()
    expect(schemaBox().getAttribute('placeholder')).toContain('"properties"')
    expect(screen.getByText(/a json schema is required/i)).toBeInTheDocument()
  })

  it('says when the schema is not JSON, and stops saying it once it is', () => {
    renderForm(AIExtractConfig, { provider: 'anthropic' })
    fireEvent.change(schemaBox(), { target: { value: '{"type": "object",' } })
    expect(saved.schema).toBe('{"type": "object",')
    expect(screen.getByText(/not valid json/i)).toBeInTheDocument()

    fireEvent.change(schemaBox(), { target: { value: '{"type": "object"}' } })
    expect(screen.queryByText(/not valid json/i)).toBeNull()
  })

  it('shows a schema that arrived as an object', () => {
    renderForm(AIExtractConfig, { provider: 'anthropic', schema: { type: 'object', properties: {} } })
    expect((schemaBox() as HTMLTextAreaElement).value).toContain('"type": "object"')
    expect(schemaBox()).not.toHaveAttribute('aria-invalid', 'true')
  })

  it('builds a schema from a list of fields', async () => {
    const user = userEvent.setup()
    renderForm(AIExtractConfig, { provider: 'anthropic' })
    await user.click(screen.getByRole('button', { name: /build from fields/i }))
    await user.click(await screen.findByRole('button', { name: /add field/i }))
    fireEvent.change(await screen.findByRole('textbox', { name: /field 1 name/i }), {
      target: { value: 'invoice_no' },
    })
    await user.click(screen.getByRole('button', { name: /use these fields/i }))

    const schema = JSON.parse(saved.schema)
    expect(schema.type).toBe('object')
    expect(schema.properties.invoice_no).toEqual({ type: 'string' })
    expect(schema.required).toEqual(['invoice_no'])
  })

  it('merges the extracted fields unless a target field is named', () => {
    renderForm(AIExtractConfig, { provider: 'anthropic', schema: '{"type":"object"}' })
    const target = screen.getByRole('textbox', { name: /target field/i })
    expect(target.getAttribute('placeholder')).toMatch(/merge/i)
    fireEvent.change(screen.getByRole('textbox', { name: /instructions/i }), {
      target: { value: 'Dates are day-first.' },
    })
    expect(saved.instructions).toBe('Dates are day-first.')
  })
})

describe('AI Embed settings', () => {
  it('says Claude cannot produce embeddings', () => {
    renderForm(AIEmbedConfig, { provider: 'anthropic' })
    expect(screen.getByText(/claude cannot produce embeddings/i)).toBeInTheDocument()
  })

  it('does not warn for a provider that embeds', () => {
    renderForm(AIEmbedConfig, { provider: 'openai', inputField: 'body' })
    expect(screen.queryByText(/cannot produce embeddings/i)).toBeNull()
  })

  it('asks what to embed when nothing is set', () => {
    renderForm(AIEmbedConfig, { provider: 'openai' })
    expect(screen.getByText(/choose the field to embed/i)).toBeInTheDocument()
  })

  // embed.go prefers inputField over text, so a template is only used once
  // the field is cleared.
  it('switches between one field and a template, clearing the other', async () => {
    const user = userEvent.setup()
    renderForm(AIEmbedConfig, { provider: 'openai', inputField: 'body' })
    expect(screen.getByRole('combobox', { name: /field to embed/i })).toHaveValue('body')

    await user.click(screen.getByRole('radio', { name: /template/i }))
    expect(saved.inputField).toBe('')
    fireEvent.change(screen.getByRole('textbox', { name: /text to embed/i }), {
      target: { value: '{{.title}} {{.body}}' },
    })
    expect(saved.text).toBe('{{.title}} {{.body}}')
  })

  it('writes the vector to embedding by default and takes no token limit', async () => {
    const user = userEvent.setup()
    renderForm(AIEmbedConfig, { provider: 'openai', inputField: 'body' })
    expect(screen.getByRole('textbox', { name: /target field/i })).toHaveAttribute('placeholder', 'embedding')
    await user.click(screen.getByRole('button', { name: /limits & timeout/i }))
    await screen.findByRole('textbox', { name: /timeout/i })
    expect(screen.queryByRole('textbox', { name: /max tokens/i })).toBeNull()
  })
})
