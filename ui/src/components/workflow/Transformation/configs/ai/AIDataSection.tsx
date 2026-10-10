import { Divider, Stack, Switch, Text, TextInput } from '@mantine/core'
import type { AISectionProps } from './AIConnectionSection'

interface AIDataSectionProps extends AISectionProps {
  /** ai_prompt only: attach the record to the prompt as JSON. */
  withIncludeData?: boolean
}

const str = (v: unknown) => (typeof v === 'string' ? v : Array.isArray(v) ? v.join(', ') : '')

/**
 * What of the record reaches the model (genai.selectInput): an allow-list,
 * fields to hide, and the PII masker. The message itself is never changed.
 */
export function AIDataSection({ config, nodeId, updateNodeConfig, withIncludeData = false }: AIDataSectionProps) {
  const set = (patch: Record<string, unknown>) => updateNodeConfig(nodeId, patch)

  return (
    <Stack gap="sm">
      <Divider label="Data sent to the model" labelPosition="center" />
      {withIncludeData && (
        <Switch
          label="Include the record"
          description="Attach the record to the prompt as JSON, after the fields below are applied."
          checked={config.includeData === true}
          onChange={(e) => set({ includeData: e.currentTarget.checked })}
        />
      )}
      <TextInput
        label="Only send these fields"
        placeholder="All fields"
        value={str(config.inputFields)}
        onChange={(e) => set({ inputFields: e.currentTarget.value })}
        description="Comma-separated. Leave empty to send every field."
      />
      <TextInput
        label="Hide these fields"
        placeholder="e.g. email, phone"
        value={str(config.maskFields)}
        onChange={(e) => set({ maskFields: e.currentTarget.value })}
        description="Comma-separated. Their values are sent as [REDACTED]."
      />
      <Switch
        label="Mask personal data"
        description="Mask emails, phone numbers, card numbers, IBANs and similar personal data in every remaining value."
        checked={config.maskPII === true}
        onChange={(e) => set({ maskPII: e.currentTarget.checked })}
      />
      {withIncludeData && (
        <Text size="xs" c="dimmed">
          These apply to the attached record. A field you name in the prompt with {'{{.field}}'} is sent as
          written.
        </Text>
      )}
    </Stack>
  )
}
