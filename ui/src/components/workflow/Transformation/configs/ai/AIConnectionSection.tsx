import { Accordion, Alert, Autocomplete, Divider, Group, NumberInput, Select, Stack, Text, TextInput } from '@mantine/core'
import { IconInfoCircle } from '@tabler/icons-react'
import { AIKeyField } from './AIKeyField'
import {
  modelPlaceholder,
  modelSuggestions,
  providerSelectData,
  requiresBaseUrl,
  showsBaseUrl,
  usesApiKey,
} from './aiProviders'

export interface AISectionProps {
  config: any
  nodeId: string
  updateNodeConfig: (id: string, config: any) => void
}

interface AIConnectionSectionProps extends AISectionProps {
  /** Embedding calls take no token limit or temperature. */
  embedding?: boolean
  /** What the node's `timeout` bounds, when it is not one call (ai_agent: the whole run). */
  timeoutHint?: { placeholder: string; description: string; error?: string }
}

const GO_DURATION = /^(\d+(\.\d+)?(ns|us|µs|ms|s|m|h))+$/

/** Numbers are saved as text: the engine reads them with core.GetConfigString. */
const asText = (v: string | number) => (v === '' ? '' : String(v))

const str = (v: unknown) => (typeof v === 'string' ? v : v == null ? '' : String(v))

const baseUrlPlaceholder = (provider: string | undefined) =>
  provider === 'ollama' ? 'http://localhost:11434/v1' : 'https://your-server.example/v1'

/**
 * Which model to call and how to reach it, shared by every AI node.
 *
 * Keys are the camelCase ones genai.connection and genai.callOptions read; the
 * fallback connection uses the same keys prefixed with `fallback`.
 */
export function AIConnectionSection({ config, nodeId, updateNodeConfig, embedding = false, timeoutHint }: AIConnectionSectionProps) {
  const set = (patch: Record<string, unknown>) => updateNodeConfig(nodeId, patch)
  const provider: string | undefined = config.provider || undefined
  const fallback: string | undefined = config.fallbackProvider || undefined
  const timeout = str(config.timeout).trim()
  const timeoutError = timeout && !GO_DURATION.test(timeout)
    ? 'The timeout needs a unit, like 60s or 2m.'
    : timeoutHint?.error

  return (
    <Stack gap="md">
      <Divider label="Model" labelPosition="center" />
      <Group grow align="flex-start" gap="sm">
        <Select
          label="Provider"
          placeholder="Choose a provider"
          data={providerSelectData}
          value={provider ?? null}
          onChange={(v) => set({ provider: v ?? '' })}
          allowDeselect={false}
          searchable
          required
        />
        <Autocomplete
          label="Model"
          placeholder={modelPlaceholder(provider)}
          data={modelSuggestions(provider)}
          value={str(config.model)}
          onChange={(v) => set({ model: v })}
          disabled={!provider}
          description="As the provider names it."
        />
      </Group>

      {!provider ? (
        <Alert icon={<IconInfoCircle size="1rem" />} color="blue" variant="light">
          <Text size="sm">
            Choose the AI provider first: Claude, ChatGPT, Gemini, a local Ollama, or any OpenAI-compatible
            server. Until then every message fails at this node.
          </Text>
        </Alert>
      ) : (
        <>
          {usesApiKey(provider) && (
            <AIKeyField
              configKey="apiKey"
              label="API key secret"
              provider={provider}
              value={config.apiKey}
              onChange={set}
            />
          )}
          {showsBaseUrl(provider) && (
            <TextInput
              label="Base URL"
              placeholder={baseUrlPlaceholder(provider)}
              value={str(config.baseUrl)}
              onChange={(e) => set({ baseUrl: e.currentTarget.value })}
              required={requiresBaseUrl(provider)}
              description={
                provider === 'ollama'
                  ? 'Where Ollama listens. Leave empty for http://localhost:11434/v1.'
                  : 'The server’s OpenAI-compatible API root, ending in /v1.'
              }
            />
          )}
        </>
      )}

      <Accordion multiple variant="separated" chevronPosition="right">
        <Accordion.Item value="limits">
          <Accordion.Control>Limits &amp; timeout</Accordion.Control>
          <Accordion.Panel>
            <Stack gap="sm">
              <Group grow align="flex-start" gap="sm">
                <TextInput
                  label="Timeout"
                  placeholder={timeoutHint?.placeholder ?? '60s'}
                  value={str(config.timeout)}
                  onChange={(e) => set({ timeout: e.currentTarget.value })}
                  error={timeoutError}
                  description={timeoutHint?.description ?? 'How long one call may take, e.g. 60s or 2m.'}
                />
                <NumberInput
                  label="Max concurrent calls"
                  placeholder="Provider default"
                  min={1}
                  allowDecimal={false}
                  value={str(config.maxConcurrency)}
                  onChange={(v) => set({ maxConcurrency: asText(v) })}
                  description="Calls this node makes at once."
                />
              </Group>
              {!embedding && (
                <Group grow align="flex-start" gap="sm">
                  <NumberInput
                    label="Max tokens"
                    placeholder="Provider default"
                    min={1}
                    allowDecimal={false}
                    value={str(config.maxTokens)}
                    onChange={(v) => set({ maxTokens: asText(v) })}
                    description="Longest answer allowed."
                  />
                  <NumberInput
                    label="Temperature"
                    placeholder="Provider default"
                    min={0}
                    max={2}
                    step={0.1}
                    decimalScale={2}
                    value={str(config.temperature)}
                    onChange={(v) => set({ temperature: asText(v) })}
                    description="0 is most predictable."
                  />
                </Group>
              )}
              {!showsBaseUrl(provider) && (
                <TextInput
                  label="Base URL override"
                  placeholder="Provider default"
                  value={str(config.baseUrl)}
                  onChange={(e) => set({ baseUrl: e.currentTarget.value })}
                  description="Only for a proxy or gateway in front of the provider."
                />
              )}
            </Stack>
          </Accordion.Panel>
        </Accordion.Item>

        <Accordion.Item value="fallback">
          <Accordion.Control>Fallback provider</Accordion.Control>
          <Accordion.Panel>
            <Stack gap="sm">
              <Text size="xs" c="dimmed">
                Used when the main provider fails or is unavailable. Leave empty for no fallback.
              </Text>
              <Group grow align="flex-start" gap="sm">
                <Select
                  label="Fallback provider"
                  placeholder="None"
                  data={providerSelectData}
                  value={fallback ?? null}
                  onChange={(v) => set({ fallbackProvider: v ?? '' })}
                  clearable
                  searchable
                />
                <Autocomplete
                  label="Fallback model"
                  placeholder={modelPlaceholder(fallback)}
                  data={modelSuggestions(fallback)}
                  value={str(config.fallbackModel)}
                  onChange={(v) => set({ fallbackModel: v })}
                  disabled={!fallback}
                />
              </Group>
              {fallback && usesApiKey(fallback) && (
                <AIKeyField
                  configKey="fallbackApiKey"
                  label="Fallback API key secret"
                  provider={fallback}
                  value={config.fallbackApiKey}
                  onChange={set}
                />
              )}
              {fallback && (
                <TextInput
                  label="Fallback base URL"
                  placeholder={showsBaseUrl(fallback) ? baseUrlPlaceholder(fallback) : 'Provider default'}
                  value={str(config.fallbackBaseUrl)}
                  onChange={(e) => set({ fallbackBaseUrl: e.currentTarget.value })}
                  required={requiresBaseUrl(fallback)}
                />
              )}
            </Stack>
          </Accordion.Panel>
        </Accordion.Item>
      </Accordion>
    </Stack>
  )
}
