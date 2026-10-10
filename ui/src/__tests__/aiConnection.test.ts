import { describe, it, expect } from 'vitest'
import {
  AI_PROVIDERS,
  providerLabel,
  modelSuggestions,
  secretToken,
  secretNameFromKey,
  isPlaintextKey,
  usesApiKey,
  requiresApiKey,
  requiresBaseUrl,
  canEmbed,
  schemaError,
} from '@/components/workflow/Transformation/configs/ai/aiProviders'

// The provider values are the `provider` strings pkg/llm/connect accepts
// (connect.Kinds plus the openai presets). A value the backend does not know
// fails every message with "unknown AI provider kind".
describe('AI provider catalogue', () => {
  it('offers exactly the providers the backend builds, with friendly names', () => {
    expect(AI_PROVIDERS.map((p) => p.value).sort()).toEqual(
      [
        'anthropic', 'openai', 'gemini', 'deepseek', 'ollama', 'mistral', 'groq',
        'openrouter', 'together', 'xai', 'openai_compatible',
      ].sort(),
    )
    expect(providerLabel('anthropic')).toBe('Claude (Anthropic)')
    expect(providerLabel('openai')).toBe('ChatGPT (OpenAI)')
    expect(providerLabel('gemini')).toBe('Gemini (Google)')
    expect(providerLabel('ollama')).toBe('Ollama (local)')
    expect(providerLabel('xai')).toBe('xAI (Grok)')
    expect(providerLabel('openai_compatible')).toBe('Other OpenAI-compatible')
  })

  it('suggests claude-opus-5-5 for Claude and hard-codes no model for the others', () => {
    expect(modelSuggestions('anthropic')).toContain('claude-opus-5-5')
    expect(modelSuggestions('openai')).toEqual([])
    expect(modelSuggestions(undefined)).toEqual([])
  })

  // connect.keyless: ollama and openai_compatible run without a key; every
  // other kind fails with "an API key is required".
  it('knows which providers need a key, and hides the key for Ollama', () => {
    expect(usesApiKey('ollama')).toBe(false)
    expect(usesApiKey('openai_compatible')).toBe(true)
    expect(requiresApiKey('openai_compatible')).toBe(false)
    expect(requiresApiKey('anthropic')).toBe(true)
    expect(requiresApiKey('groq')).toBe(true)
  })

  it('requires a base URL only for the generic OpenAI-compatible kind', () => {
    expect(requiresBaseUrl('openai_compatible')).toBe(true)
    expect(requiresBaseUrl('ollama')).toBe(false)
    expect(requiresBaseUrl('openai')).toBe(false)
  })

  it('says Claude cannot embed while OpenAI, Gemini and Ollama can', () => {
    expect(canEmbed('anthropic')).toBe(false)
    for (const p of ['openai', 'gemini', 'ollama', 'openai_compatible']) {
      expect(canEmbed(p), p).toBe(true)
    }
  })
})

// The key is resolved with evaluator.ResolveTemplateScoped, so the node stores
// a {{secret("NAME")}} token and the value stays in the vhost's secrets.
describe('API key as a vhost secret', () => {
  it('writes the secret token the backend resolves', () => {
    expect(secretToken('OPENAI_KEY')).toBe('{{secret("OPENAI_KEY")}}')
    expect(secretToken('  OPENAI_KEY ')).toBe('{{secret("OPENAI_KEY")}}')
    expect(secretToken('')).toBe('')
  })

  it('reads the secret name back from the token', () => {
    expect(secretNameFromKey('{{secret("OPENAI_KEY")}}')).toBe('OPENAI_KEY')
    expect(secretNameFromKey('{{ secret("A_B") }}')).toBe('A_B')
    expect(secretNameFromKey("{{secret('X')}}")).toBe('X')
    expect(secretNameFromKey('sk-live-123')).toBe('')
    expect(secretNameFromKey(undefined)).toBe('')
  })

  // The same rule as workflow_validation_ai.go: a key with no "{{" is stored
  // in the workflow and travels with every export.
  it('flags a key typed into the node as plaintext', () => {
    expect(isPlaintextKey('sk-live-123')).toBe(true)
    expect(isPlaintextKey('{{secret("K")}}')).toBe(false)
    expect(isPlaintextKey('')).toBe(false)
    expect(isPlaintextKey(undefined)).toBe(false)
  })
})

describe('extract schema check', () => {
  it('accepts a JSON object and reports what is wrong otherwise', () => {
    expect(schemaError('{"type":"object"}')).toBe('')
    expect(schemaError({ type: 'object' })).toBe('')
    expect(schemaError('')).toMatch(/required/i)
    expect(schemaError('{not json')).toMatch(/not valid JSON/i)
    expect(schemaError('[1,2]')).toMatch(/object/i)
  })
})
