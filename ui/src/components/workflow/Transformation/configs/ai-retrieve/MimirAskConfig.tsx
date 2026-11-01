import { FormRow } from '@/components/common/FormRow'
import { useMemo, useState } from 'react'
import { Alert, Autocomplete, Code, Divider, Input, NumberInput, SegmentedControl, Stack, Text } from '@mantine/core'
import { TextInput } from '@/components/shared/ReferenceInputs'
import { IconInfoCircle } from '@tabler/icons-react'
import { TemplateField } from '@/components/shared/TemplateField'
import { AIKeyField } from '../ai/AIKeyField'

const str = (v: unknown) => (typeof v === 'string' ? v : v == null ? '' : String(v))
/** Numbers are saved as text: the engine reads them with core.GetConfigString. */
const asText = (v: string | number) => (v === '' ? '' : String(v))

/** The engine's defaults and ceilings (pkg/comm/transformer/genai/mimirask). */
export const MIMIR_ASK_DEFAULTS = { mode: 'ask', sources: 3, limit: 5, maxLimit: 50, targetField: 'answer', timeout: '60s' } as const

interface MimirAskConfigProps {
  config: Record<string, any>
  nodeId: string
  updateNodeConfig: (nodeId: string, patch: Record<string, unknown>) => void
  availableFields?: any[]
}

/**
 * mimir_ask: ask a mimir-server document collection a question about the
 * record and keep the answer with the passages it cites (ask.go). The
 * server's address and key are the same ones a mimir model connected on
 * the Machine Learning page uses; the key is a vhost secret.
 */
export function MimirAskConfig({ config, nodeId, updateNodeConfig, availableFields = [] }: MimirAskConfigProps) {
  const set = (patch: Record<string, unknown>) => updateNodeConfig(nodeId, patch)
  const fieldPaths = useMemo(
    () => (availableFields || []).map((f: any) => (typeof f === 'string' ? f : f?.path)).filter(Boolean) as string[],
    [availableFields],
  )
  const mode = str(config.mode) || MIMIR_ASK_DEFAULTS.mode
  const questionField = str(config.questionField)
  const [source, setSource] = useState<'template' | 'field'>(questionField ? 'field' : 'template')
  const chooseSource = (m: string) => {
    setSource(m as 'template' | 'field')
    // questionField wins over question in the engine, so only one is kept.
    set(m === 'field' ? { question: '' } : { questionField: '' })
  }
  const missing = (v: unknown) => (str(v).trim() ? undefined : 'Required')

  return (
    <Stack gap="md">
      <Alert icon={<IconInfoCircle size="1rem" />} color="grape" variant="light">
        <Text size="sm">
          Asks the documents a team keeps on its mimir server a question about each record, and adds the answer with
          the passages it cites. Choose <em>Passages only</em> to hand the best passages to an AI Prompt instead.
        </Text>
      </Alert>

      <Divider label="mimir server" labelPosition="center" />
      <TextInput
        label="Server address"
        placeholder="http://mimir:8080"
        value={str(config.url)}
        onChange={(e) => set({ url: e.currentTarget.value })}
        required
        error={missing(config.url)}
        description={
          <>
            The address a model connected on the Machine Learning page uses. <Code>{'{{secret.NAME}}'}</Code> works here too.
          </>
        }
      />
      <AIKeyField
        configKey="apiKey"
        label="API key secret"
        provider={undefined}
        required
        example="MIMIR_API_KEY"
        value={config.apiKey}
        onChange={set}
      />
      <TextInput
        label="Collection"
        placeholder="The collection's id from the server's Documents page"
        value={str(config.collection)}
        onChange={(e) => set({ collection: e.currentTarget.value })}
        required
        error={missing(config.collection)}
        description="The documents to ask. One node asks one collection."
      />

      <Divider label="Question" labelPosition="center" />
      <Input.Wrapper label="What to do">
        <SegmentedControl
          mt={4}
          fullWidth
          value={mode}
          onChange={(v) => set({ mode: v })}
          data={[
            { label: 'Answer the question', value: 'ask' },
            { label: 'Passages only', value: 'search' },
          ]}
        />
      </Input.Wrapper>
      <Input.Wrapper label="The question is">
        <SegmentedControl
          mt={4}
          fullWidth
          value={source}
          onChange={chooseSource}
          data={[
            { label: 'A template', value: 'template' },
            { label: 'A field', value: 'field' },
          ]}
        />
      </Input.Wrapper>
      {source === 'template' ? (
        <TemplateField
          label="Question template"
          placeholder={'e.g. Can {{.customer.name}} return a {{.item}} after {{.days}} days?'}
          value={str(config.question)}
          onChange={(question) => set({ question })}
          availableFields={fieldPaths}
          functions="token"
          multiline
          error={missing(config.question)}
          description="Insert fields of the record from the buttons in the box."
        />
      ) : (
        <Autocomplete
          label="Question field"
          placeholder="e.g. ticket.body"
          data={fieldPaths}
          value={questionField}
          onChange={(v) => set({ questionField: v })}
          error={missing(questionField)}
          description="Its value is asked as it is."
        />
      )}

      <Divider label="Result" labelPosition="center" />
      <FormRow>
        {mode === 'search' ? (
          <NumberInput
            label="Passages"
            placeholder={String(MIMIR_ASK_DEFAULTS.limit)}
            min={1}
            max={MIMIR_ASK_DEFAULTS.maxLimit}
            allowDecimal={false}
            value={str(config.limit)}
            onChange={(v) => set({ limit: asText(v) })}
            description={`How many passages to keep, best first. At most ${MIMIR_ASK_DEFAULTS.maxLimit}.`}
          />
        ) : (
          <>
            <NumberInput
              label="Sources"
              placeholder={String(MIMIR_ASK_DEFAULTS.sources)}
              min={1}
              max={MIMIR_ASK_DEFAULTS.maxLimit}
              allowDecimal={false}
              value={str(config.sources)}
              onChange={(v) => set({ sources: asText(v) })}
              description="Passages the answer may cite."
            />
            <NumberInput
              label="Longest answer"
              placeholder="The server's default"
              min={1}
              allowDecimal={false}
              value={str(config.maxTokens)}
              onChange={(v) => set({ maxTokens: asText(v) })}
              description="In tokens. An answer cut short says finished: false."
            />
          </>
        )}
      </FormRow>
      <FormRow>
        <TextInput
          label="Target field"
          placeholder={MIMIR_ASK_DEFAULTS.targetField}
          value={str(config.targetField)}
          onChange={(e) => set({ targetField: e.currentTarget.value })}
          description={
            mode === 'search'
              ? 'Receives {sources: [{document, document_id, passage_id, number, score, text}]}.'
              : 'Receives {text, finished, sources: [{document, document_id, passage_id, number, score, text}]}.'
          }
        />
        <TextInput
          label="Timeout"
          placeholder={MIMIR_ASK_DEFAULTS.timeout}
          value={str(config.timeout)}
          onChange={(e) => set({ timeout: e.currentTarget.value })}
          description="How long one question may take, e.g. 30s."
        />
      </FormRow>
    </Stack>
  )
}
