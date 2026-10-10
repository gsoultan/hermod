/**
 * What the AI node editors know about providers, keys and settings.
 *
 * Every rule here mirrors the backend so the form cannot offer something the
 * engine rejects:
 *  - provider values are pkg/llm/connect's kinds (connect.Kinds plus the
 *    openai presets in pkg/llm/openai);
 *  - ollama and openai_compatible are connect's `keyless` kinds, every other
 *    kind fails with "an API key is required";
 *  - openai_compatible fails without a base URL;
 *  - the key is resolved with evaluator.ResolveTemplateScoped, so a vhost
 *    secret is referenced as {{secret("NAME")}};
 *  - a key without "{{" is the plaintext key workflow_validation_ai.go warns
 *    about.
 */

export interface AIProvider {
  value: string
  label: string
  /** Placeholder text for the model field — shown only, never saved. */
  example: string
  /** Whether the provider answers embedding requests (llm.Embedder). */
  embeds: boolean
}

export const AI_PROVIDERS: AIProvider[] = [
  { value: 'anthropic', label: 'Claude (Anthropic)', example: 'e.g. claude-opus-5-5', embeds: false },
  { value: 'openai', label: 'ChatGPT (OpenAI)', example: 'The model name from your OpenAI account', embeds: true },
  { value: 'gemini', label: 'Gemini (Google)', example: 'The model name from Google AI Studio', embeds: true },
  { value: 'deepseek', label: 'DeepSeek', example: 'The model name from your DeepSeek account', embeds: true },
  { value: 'ollama', label: 'Ollama (local)', example: 'A model you have pulled, as `ollama list` shows it', embeds: true },
  { value: 'mistral', label: 'Mistral', example: 'The model name from your Mistral account', embeds: true },
  { value: 'groq', label: 'Groq', example: 'The model name from your Groq console', embeds: true },
  { value: 'openrouter', label: 'OpenRouter', example: 'vendor/model, as OpenRouter lists it', embeds: true },
  { value: 'together', label: 'Together', example: 'The model name from your Together account', embeds: true },
  { value: 'xai', label: 'xAI (Grok)', example: 'The model name from your xAI console', embeds: true },
  { value: 'openai_compatible', label: 'Other OpenAI-compatible', example: 'The model name your server serves', embeds: true },
]

const byValue = new Map(AI_PROVIDERS.map((p) => [p.value, p]))

export const providerSelectData = AI_PROVIDERS.map(({ value, label }) => ({ value, label }))

export function providerLabel(value: string | undefined): string {
  return (value && byValue.get(value)?.label) || value || ''
}

/** The model-name placeholder for a provider. */
export function modelPlaceholder(value: string | undefined): string {
  const p = value ? byValue.get(value) : undefined
  return p ? p.example : 'Choose a provider first'
}

/**
 * Model names offered in the model field's dropdown. Only Claude's is named:
 * the others change too often to hard-code, so they get a placeholder instead.
 */
export function modelSuggestions(value: string | undefined): string[] {
  return value === 'anthropic' ? ['claude-opus-5-5'] : []
}

const KEYLESS = new Set(['ollama', 'openai_compatible'])

/** Whether the key field is shown at all. Ollama runs locally without one. */
export function usesApiKey(provider: string | undefined): boolean {
  return provider !== 'ollama'
}

/** Whether the engine refuses to call the provider without a key. */
export function requiresApiKey(provider: string | undefined): boolean {
  return !!provider && !KEYLESS.has(provider)
}

export function requiresBaseUrl(provider: string | undefined): boolean {
  return provider === 'openai_compatible'
}

/** Whether the base URL is shown up front rather than under Advanced. */
export function showsBaseUrl(provider: string | undefined): boolean {
  return provider === 'ollama' || provider === 'openai_compatible'
}

export function canEmbed(provider: string | undefined): boolean {
  return !!provider && (byValue.get(provider)?.embeds ?? true)
}

export function secretToken(name: string): string {
  const n = name.trim()
  return n ? `{{secret("${n}")}}` : ''
}

const SECRET_TOKEN = /^\{\{\s*secret\(\s*["']([^"']+)["']\s*\)\s*\}\}$/

/** The NAME in a {{secret("NAME")}} key, or '' when the key is anything else. */
export function secretNameFromKey(key: unknown): string {
  if (typeof key !== 'string') return ''
  return SECRET_TOKEN.exec(key.trim())?.[1] ?? ''
}

/** A key typed into the node: saved with the workflow and exported with it. */
export function isPlaintextKey(key: unknown): boolean {
  return typeof key === 'string' && key.trim() !== '' && !key.includes('{{')
}

/**
 * What is wrong with an ai_extract schema, or '' when it is usable. The engine
 * (extract.go loadSchema) accepts the schema as JSON text or a decoded object.
 */
export function schemaError(raw: unknown): string {
  if (raw && typeof raw === 'object' && !Array.isArray(raw)) return ''
  if (typeof raw !== 'string' || raw.trim() === '') return 'A JSON Schema is required.'
  let parsed: unknown
  try {
    parsed = JSON.parse(raw)
  } catch (e) {
    return `The schema is not valid JSON: ${(e as Error).message}`
  }
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
    return 'The schema must be a JSON object, such as {"type": "object", ...}.'
  }
  return ''
}
