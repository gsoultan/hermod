import { useMemo, useState } from 'react'
import { Alert, Autocomplete, Divider, Input, SegmentedControl, Stack, Text, Textarea, TextInput } from '@mantine/core'
import { IconAlertTriangle, IconInfoCircle } from '@tabler/icons-react'
import { AIConnectionSection, type AISectionProps } from './AIConnectionSection'
import { canEmbed, providerLabel } from './aiProviders'

const str = (v: unknown) => (typeof v === 'string' ? v : '')

/** Where the vector goes when Target Field is empty (genai.DefaultEmbeddingField). */
const DEFAULT_EMBEDDING_FIELD = 'embedding'

interface AIEmbedConfigProps extends AISectionProps {
  availableFields?: any[]
}

/**
 * ai_embed: turn one field, or a template over the record, into a vector for
 * a pgvector, Pinecone or Milvus sink. embed.go reads inputField first and
 * the text template only when no field is set.
 */
export function AIEmbedConfig({ config, nodeId, updateNodeConfig, availableFields = [] }: AIEmbedConfigProps) {
  const set = (patch: Record<string, unknown>) => updateNodeConfig(nodeId, patch)
  const fieldPaths = useMemo(
    () => (availableFields || []).map((f: any) => (typeof f === 'string' ? f : f?.path)).filter(Boolean) as string[],
    [availableFields],
  )
  const inputField = str(config.inputField)
  const text = str(config.text)
  const [mode, setMode] = useState<'field' | 'template'>(!inputField && text ? 'template' : 'field')
  const nothing = inputField.trim() === '' && text.trim() === ''
  const provider: string | undefined = config.provider || undefined

  const chooseMode = (m: string) => {
    setMode(m as 'field' | 'template')
    // Only one source may be set: a leftover field would win over the template.
    set(m === 'template' ? { inputField: '' } : { text: '' })
  }

  return (
    <Stack gap="md">
      <AIConnectionSection config={config} nodeId={nodeId} updateNodeConfig={updateNodeConfig} embedding />
      {provider && !canEmbed(provider) && (
        <Alert color="red" variant="light" icon={<IconAlertTriangle size="1rem" />} title="Choose another provider">
          <Text size="sm">
            {providerLabel(provider).split(' (')[0]} cannot produce embeddings, so every message would fail here. Use
            OpenAI, Gemini, Ollama or another OpenAI-compatible provider with an embedding model.
          </Text>
        </Alert>
      )}

      <Divider label="What to embed" labelPosition="center" />
      <Input.Wrapper label="Embed">
        <SegmentedControl
          mt={4}
          fullWidth
          value={mode}
          onChange={chooseMode}
          data={[
            { label: 'One field', value: 'field' },
            { label: 'A template', value: 'template' },
          ]}
        />
      </Input.Wrapper>
      {mode === 'field' ? (
        <Autocomplete
          label="Field to embed"
          placeholder="e.g. description"
          data={fieldPaths}
          value={inputField}
          onChange={(v) => set({ inputField: v })}
          description="Its value is sent as it is."
        />
      ) : (
        <Textarea
          label="Text to embed"
          placeholder={'e.g. {{.title}}. {{.description}}'}
          value={text}
          onChange={(e) => set({ text: e.currentTarget.value })}
          autosize
          minRows={2}
          description={'Use {{.field}} to insert a field of the record.'}
        />
      )}
      {nothing && (
        <Alert color="blue" variant="light" icon={<IconInfoCircle size="1rem" />}>
          <Text size="sm">
            Choose the field to embed, or write a template. A message with nothing to embed fails at this node.
          </Text>
        </Alert>
      )}
      <TextInput
        label="Target field"
        placeholder={DEFAULT_EMBEDDING_FIELD}
        value={str(config.targetField)}
        onChange={(e) => set({ targetField: e.currentTarget.value })}
        description={`Field that receives the vector. Empty means ${DEFAULT_EMBEDDING_FIELD}.`}
      />
    </Stack>
  )
}
