import { FormRow } from '@/components/common/FormRow'
import { Divider, Input, SegmentedControl, Stack } from '@mantine/core'
import { TextInput } from '@/components/shared/ReferenceInputs'
import { TemplateField } from '@/components/shared/TemplateField'
import { AIConnectionSection, type AISectionProps } from './AIConnectionSection'
import { AIDataSection } from './AIDataSection'

const str = (v: unknown) => (typeof v === 'string' ? v : '')

/** Where an answer goes when Target Field is empty (genai.DefaultPromptField). */
const DEFAULT_TEXT_FIELD = 'ai_output'

/**
 * ai_prompt: send a prompt built from the record and write the answer back —
 * as text into one field, or as a JSON object merged into (or nested under a
 * field of) the record.
 */
export function AIPromptConfig({ config, nodeId, updateNodeConfig, fieldPaths = [] }: AISectionProps) {
  const set = (patch: Record<string, unknown>) => updateNodeConfig(nodeId, patch)
  const json = config.outputMode === 'json'
  const promptMissing = str(config.prompt).trim() === ''

  return (
    <Stack gap="md">
      <AIConnectionSection config={config} nodeId={nodeId} updateNodeConfig={updateNodeConfig} />

      <Divider label="Prompt" labelPosition="center" />
      <TemplateField
        label="System instructions"
        placeholder="e.g. You are a support analyst. Answer briefly."
        value={str(config.system)}
        onChange={(system) => set({ system })}
        availableFields={fieldPaths}
        functions="token"
        multiline
        description="Optional. How the model should behave, sent before the prompt. Fields, secrets and functions work here too."
      />
      <TemplateField
        label="Prompt"
        placeholder={'e.g. Classify the sentiment of this review: {{.comment}}'}
        value={str(config.prompt)}
        onChange={(prompt) => set({ prompt })}
        availableFields={fieldPaths}
        functions="token"
        multiline
        required
        error={promptMissing ? 'A prompt is required.' : undefined}
        description="Insert fields of the record, secrets and functions from the buttons in the box."
      />

      <Divider label="Answer" labelPosition="center" />
      <Input.Wrapper label="Answer format" description="JSON asks for one JSON object and fails the message if none comes back.">
        <SegmentedControl
          mt={4}
          fullWidth
          value={json ? 'json' : 'text'}
          onChange={(v) => set({ outputMode: v })}
          data={[
            { label: 'Text', value: 'text' },
            { label: 'JSON', value: 'json' },
          ]}
        />
      </Input.Wrapper>
      <FormRow>
        <TextInput
          label="Target field"
          placeholder={json ? 'Empty: merge into the record' : DEFAULT_TEXT_FIELD}
          value={str(config.targetField)}
          onChange={(e) => set({ targetField: e.currentTarget.value })}
          description={
            json
              ? 'Leave empty to merge the answer’s keys into the record, or name a field to nest it.'
              : `Field that receives the answer. Empty means ${DEFAULT_TEXT_FIELD}.`
          }
        />
        <TextInput
          label="Usage field"
          placeholder="Not recorded"
          value={str(config.usageField)}
          onChange={(e) => set({ usageField: e.currentTarget.value })}
          description="Optional. Records provider, model and token counts."
        />
      </FormRow>

      <AIDataSection config={config} nodeId={nodeId} updateNodeConfig={updateNodeConfig} withIncludeData />
    </Stack>
  )
}
