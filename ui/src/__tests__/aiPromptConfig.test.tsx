import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { useState } from 'react'
import { describe, it, expect } from 'vitest'
import { SecretNamesContext } from '@/components/shared/TemplateField'
import { AIPromptConfig } from '@/components/workflow/Transformation/configs/ai/AIPromptConfig'

// The editor merges every patch into the node's config (useWorkflowStore
// updateNodeConfig), so the harness does the same and exposes the result.
let saved: Record<string, any> = {}

function Harness({ initial }: { initial: Record<string, any> }) {
  const [config, setConfig] = useState(initial)
  return (
    <AIPromptConfig
      config={config}
      nodeId="n1"
      updateNodeConfig={(_id: string, patch: any) => setConfig((c: any) => (saved = { ...c, ...patch }))}
    />
  )
}

const renderPrompt = (initial: Record<string, any>, secretNames: string[] = []) => {
  saved = initial
  return render(
    <MantineProvider>
      <SecretNamesContext.Provider value={secretNames}>
        <Harness initial={initial} />
      </SecretNamesContext.Provider>
    </MantineProvider>,
  )
}

const keyInput = () => screen.getByRole('combobox', { name: /api key secret/i }) as HTMLInputElement

describe('AI connection settings', () => {
  it('asks for a provider before anything else', () => {
    renderPrompt({})
    expect(screen.getByRole('combobox', { name: /^provider/i })).toBeInTheDocument()
    expect(screen.getByText(/choose the ai provider/i)).toBeInTheDocument()
    expect(screen.queryByRole('combobox', { name: /api key secret/i })).toBeNull()
  })

  it('offers friendly provider names', async () => {
    const user = userEvent.setup()
    renderPrompt({})
    const provider = screen.getByRole('combobox', { name: /^provider/i })
    await user.click(provider)
    expect(provider).toHaveAttribute('aria-expanded', 'true')
    expect(screen.getByText('Claude (Anthropic)')).toBeInTheDocument()
    expect(screen.getByText('Other OpenAI-compatible')).toBeInTheDocument()
    await user.click(screen.getByText('Claude (Anthropic)'))
    expect(saved.provider).toBe('anthropic')
  })

  it('stores the key as a vhost secret reference, never the key itself', () => {
    renderPrompt({ provider: 'openai' })
    fireEvent.change(keyInput(), { target: { value: 'OPENAI_KEY' } })
    expect(saved.apiKey).toBe('{{secret("OPENAI_KEY")}}')
  })

  it('reads the secret name back from a saved reference', () => {
    renderPrompt({ provider: 'anthropic', apiKey: '{{secret("CLAUDE_KEY")}}' })
    expect(keyInput().value).toBe('CLAUDE_KEY')
    expect(screen.queryByText(/stored in the workflow/i)).toBeNull()
  })

  it('offers the vhost secrets that already exist', async () => {
    const user = userEvent.setup()
    renderPrompt({ provider: 'openai' }, ['OPENAI_KEY', 'OTHER'])
    await user.click(keyInput())
    await user.click(screen.getByText('OPENAI_KEY'))
    expect(saved.apiKey).toBe('{{secret("OPENAI_KEY")}}')
  })

  it('rejects a name a vhost secret cannot have', () => {
    renderPrompt({ provider: 'openai' })
    fireEvent.change(keyInput(), { target: { value: 'my key' } })
    expect(screen.getByText(/letters, digits and underscores/i)).toBeInTheDocument()
  })

  it('warns about a key typed into the node and can clear it', async () => {
    const user = userEvent.setup()
    renderPrompt({ provider: 'openai', apiKey: 'sk-live-123' })
    expect(screen.getByText(/stored in the workflow/i)).toBeInTheDocument()
    expect(screen.getByText(/export/i)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: /clear stored key/i }))
    expect(saved.apiKey).toBe('')
    expect(screen.queryByText(/stored in the workflow/i)).toBeNull()
  })

  it('hides the key for Ollama and asks for its address', () => {
    renderPrompt({ provider: 'ollama' })
    expect(screen.queryByRole('combobox', { name: /api key secret/i })).toBeNull()
    expect(screen.getByRole('textbox', { name: /base url/i })).toBeInTheDocument()
  })

  it('requires a base URL for another OpenAI-compatible server, where the key is optional', () => {
    renderPrompt({ provider: 'openai_compatible' })
    expect(screen.getByRole('textbox', { name: /base url/i })).toBeRequired()
    expect(keyInput()).not.toBeRequired()
  })

  it('requires the key for hosted providers', () => {
    renderPrompt({ provider: 'groq' })
    expect(keyInput()).toBeRequired()
  })

  it('suggests claude-opus-5-5 for Claude', async () => {
    const user = userEvent.setup()
    renderPrompt({ provider: 'anthropic' })
    await user.click(screen.getByRole('combobox', { name: /^model/i }))
    await user.click(screen.getByText('claude-opus-5-5'))
    expect(saved.model).toBe('claude-opus-5-5')
  })

  // core.GetConfigString reads only strings: a number saved as a number is
  // silently ignored by the engine.
  it('saves numeric limits as the strings the engine reads', async () => {
    const user = userEvent.setup()
    renderPrompt({ provider: 'anthropic' })
    await user.click(screen.getByRole('button', { name: /limits & timeout/i }))
    fireEvent.change(await screen.findByRole('textbox', { name: /max tokens/i }), { target: { value: '512' } })
    fireEvent.change(screen.getByRole('textbox', { name: /timeout/i }), { target: { value: '90s' } })
    expect(saved.maxTokens).toBe('512')
    expect(saved.timeout).toBe('90s')
  })

  it('rejects a timeout that is not a Go duration', async () => {
    const user = userEvent.setup()
    renderPrompt({ provider: 'anthropic', timeout: '90' })
    await user.click(screen.getByRole('button', { name: /limits & timeout/i }))
    expect(await screen.findByRole('textbox', { name: /timeout/i })).toHaveAttribute('aria-invalid', 'true')
    expect(screen.getByText(/needs a unit/i)).toBeInTheDocument()
  })

  it('configures a fallback provider with its own secret', async () => {
    const user = userEvent.setup()
    renderPrompt({ provider: 'anthropic', fallbackProvider: 'openai' })
    await user.click(screen.getByRole('button', { name: /fallback provider/i }))
    fireEvent.change(await screen.findByRole('combobox', { name: /fallback api key secret/i }), {
      target: { value: 'OPENAI_KEY' },
    })
    expect(saved.fallbackApiKey).toBe('{{secret("OPENAI_KEY")}}')
  })
})

describe('data sent to the model', () => {
  it('saves the field lists as comma-separated text and the switches as booleans', async () => {
    const user = userEvent.setup()
    renderPrompt({ provider: 'anthropic' })
    fireEvent.change(screen.getByRole('textbox', { name: /only send these fields/i }), {
      target: { value: 'name, comment' },
    })
    fireEvent.change(screen.getByRole('textbox', { name: /hide these fields/i }), {
      target: { value: 'email' },
    })
    await user.click(screen.getByRole('switch', { name: /mask personal data/i }))
    await user.click(screen.getByRole('switch', { name: /include the record/i }))
    expect(saved.inputFields).toBe('name, comment')
    expect(saved.maskFields).toBe('email')
    expect(saved.maskPII).toBe(true)
    expect(saved.includeData).toBe(true)
  })
})

describe('AI prompt settings', () => {
  it('requires a prompt and explains the field tokens', () => {
    renderPrompt({ provider: 'anthropic' })
    const prompt = screen.getByRole('textbox', { name: /^prompt/i })
    expect(prompt).toBeRequired()
    expect(screen.getByText(/a prompt is required/i)).toBeInTheDocument()
    fireEvent.change(prompt, { target: { value: 'Summarise {{.comment}}' } })
    expect(saved.prompt).toBe('Summarise {{.comment}}')
    expect(screen.queryByText(/a prompt is required/i)).toBeNull()
  })

  it('writes text answers to ai_output by default and merges JSON answers', async () => {
    const user = userEvent.setup()
    renderPrompt({ provider: 'anthropic', prompt: 'x' })
    const target = screen.getByRole('textbox', { name: /target field/i })
    expect(target).toHaveAttribute('placeholder', 'ai_output')
    await user.click(screen.getByRole('radio', { name: /json/i }))
    expect(saved.outputMode).toBe('json')
    expect(screen.getByRole('textbox', { name: /target field/i }).getAttribute('placeholder')).toMatch(/merge/i)
  })
})
