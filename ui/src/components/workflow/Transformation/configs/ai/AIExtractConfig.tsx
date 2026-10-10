import { useState } from 'react'
import { Button, Divider, Group, Stack, Textarea, TextInput } from '@mantine/core'
import { IconListDetails } from '@tabler/icons-react'
import { AIConnectionSection, type AISectionProps } from './AIConnectionSection'
import { AIDataSection } from './AIDataSection'
import { AISchemaBuilder } from './AISchemaBuilder'
import { schemaError } from './aiProviders'

const str = (v: unknown) => (typeof v === 'string' ? v : '')

const EXAMPLE_SCHEMA = `{
  "type": "object",
  "properties": {
    "invoice_no": { "type": "string" },
    "total": { "type": "number", "description": "Amount due, in the invoice currency" }
  },
  "required": ["invoice_no", "total"]
}`

/** The schema as text: the engine also accepts it as an already-decoded object. */
function schemaText(raw: unknown): string {
  if (raw && typeof raw === 'object') return JSON.stringify(raw, null, 2)
  return str(raw)
}

/**
 * ai_extract: pull structured fields out of free text. The answer is checked
 * against the JSON Schema and retried once with the problems, so the schema
 * is the contract for what lands on the record.
 */
export function AIExtractConfig({ config, nodeId, updateNodeConfig }: AISectionProps) {
  const set = (patch: Record<string, unknown>) => updateNodeConfig(nodeId, patch)
  const [building, setBuilding] = useState(false)
  const text = schemaText(config.schema)
  const error = schemaError(config.schema)

  return (
    <Stack gap="md">
      <AIConnectionSection config={config} nodeId={nodeId} updateNodeConfig={updateNodeConfig} />

      <Divider label="What to extract" labelPosition="center" />
      <Textarea
        label="JSON Schema"
        placeholder={EXAMPLE_SCHEMA}
        value={text}
        onChange={(e) => set({ schema: e.currentTarget.value })}
        autosize
        minRows={8}
        maxRows={20}
        required
        error={error || undefined}
        styles={{ input: { fontFamily: 'var(--mantine-font-family-monospace)' } }}
        description="The fields to extract and their types. The answer is checked against it."
      />
      <Group>
        <Button
          size="xs"
          variant="subtle"
          leftSection={<IconListDetails size="0.9rem" />}
          onClick={() => setBuilding((b) => !b)}
          aria-expanded={building}
        >
          {building ? 'Hide field builder' : 'Build from fields'}
        </Button>
      </Group>
      {building && (
        <AISchemaBuilder
          replaces={text.trim() !== ''}
          onApply={(schema) => {
            set({ schema })
            setBuilding(false)
          }}
        />
      )}
      <Textarea
        label="Instructions"
        placeholder="e.g. Dates are day-first. Amounts are in euros."
        value={str(config.instructions)}
        onChange={(e) => set({ instructions: e.currentTarget.value })}
        autosize
        minRows={2}
        description="Optional. Anything the model should know about the input."
      />
      <Group grow align="flex-start" gap="sm">
        <TextInput
          label="Target field"
          placeholder="Empty: merge into the record"
          value={str(config.targetField)}
          onChange={(e) => set({ targetField: e.currentTarget.value })}
          description="Leave empty to add the extracted fields to the record, or name a field to nest them."
        />
        <TextInput
          label="Usage field"
          placeholder="Not recorded"
          value={str(config.usageField)}
          onChange={(e) => set({ usageField: e.currentTarget.value })}
          description="Optional. Records provider, model and token counts."
        />
      </Group>

      <AIDataSection config={config} nodeId={nodeId} updateNodeConfig={updateNodeConfig} />
    </Stack>
  )
}
