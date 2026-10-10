import { useMemo, useState } from 'react'
import {
  Alert, Autocomplete, Code, Divider, Group, Input, NumberInput, SegmentedControl, Select, Stack, Text, Textarea, TextInput,
} from '@mantine/core'
import { IconAlertTriangle, IconInfoCircle } from '@tabler/icons-react'
import { AIConnectionSection, type AISectionProps } from '../ai/AIConnectionSection'
import { AIKeyField } from '../ai/AIKeyField'
import { canEmbed, providerLabel } from '../ai/aiProviders'
import { PGVECTOR_METRICS, RETRIEVE_DEFAULTS, retrieveIssues, retrieveStore } from './retrieveIssues'

const str = (v: unknown) => (typeof v === 'string' ? v : v == null ? '' : String(v))

/** Numbers are saved as text: the engine reads them with core.GetConfigString. */
const asText = (v: string | number) => (v === '' ? '' : String(v))

interface AIRetrieveConfigProps extends AISectionProps {
  availableFields?: any[]
}

const STORE_OPTIONS = [
  { value: 'pgvector', label: 'pgvector (PostgreSQL)' },
  { value: 'pinecone', label: 'Pinecone' },
]

/** A credential-bearing value typed in rather than taken from a secret. */
const typedIn = (v: unknown) => typeof v === 'string' && v.trim() !== '' && !v.includes('{{')

/**
 * ai_retrieve: embed a query with the node's AI connection, ask a pgvector
 * table or a Pinecone index for the nearest documents, and write them with
 * their scores to the record (retrieve.go). Keys match the transformer's.
 */
export function AIRetrieveConfig({ config, nodeId, updateNodeConfig, availableFields = [] }: AIRetrieveConfigProps) {
  const set = (patch: Record<string, unknown>) => updateNodeConfig(nodeId, patch)
  const fieldPaths = useMemo(
    () => (availableFields || []).map((f: any) => (typeof f === 'string' ? f : f?.path)).filter(Boolean) as string[],
    [availableFields],
  )
  const provider: string | undefined = config.provider || undefined
  const store = retrieveStore(config.store)
  const issues = retrieveIssues(config)
  const errorFor = (field: string) => issues.find((i) => i.field === field && i.severity === 'error')?.message
  const queryField = str(config.queryField)
  const [mode, setMode] = useState<'template' | 'field'>(queryField ? 'field' : 'template')

  const chooseMode = (m: string) => {
    setMode(m as 'template' | 'field')
    // queryField wins over query in the engine, so only one is kept.
    set(m === 'field' ? { query: '' } : { queryField: '' })
  }

  return (
    <Stack gap="md">
      <Alert icon={<IconInfoCircle size="1rem" />} color="grape" variant="light">
        <Text size="sm">
          Finds the documents closest to a query in a vector store and adds them, with their scores, to the record —
          ready for an AI Prompt or an AI Agent that follows.
        </Text>
      </Alert>

      <AIConnectionSection
        config={config}
        nodeId={nodeId}
        updateNodeConfig={updateNodeConfig}
        embedding
        timeoutHint={{
          placeholder: RETRIEVE_DEFAULTS.timeout,
          description: 'Embedding the query and searching the store together, e.g. 30s.',
        }}
      />
      {provider && !canEmbed(provider) && (
        <Alert color="red" variant="light" icon={<IconAlertTriangle size="1rem" />} title="Choose another provider">
          <Text size="sm">
            {providerLabel(provider).split(' (')[0]} cannot produce embeddings, so every message would fail here. Use the
            provider and embedding model that filled the store.
          </Text>
        </Alert>
      )}

      <Divider label="Vector store" labelPosition="center" />
      <Select
        label="Store"
        placeholder="Choose a store"
        data={STORE_OPTIONS}
        value={store || null}
        onChange={(v) => set({ store: v ?? '' })}
        allowDeselect={false}
        required
        error={errorFor('store')}
        description="Where the documents and their vectors were written, e.g. by a pgvector or Pinecone sink."
      />

      {store === 'pgvector' && (
        <>
          <TextInput
            label="Connection string"
            placeholder={'{{secret("PGVECTOR_URL")}}'}
            value={str(config.connectionString)}
            onChange={(e) => set({ connectionString: e.currentTarget.value })}
            required
            error={errorFor('connectionString')}
            description={
              <>
                A PostgreSQL URL. Use <Code>{'{{secret("NAME")}}'}</Code> to read it from a vhost secret; record fields
                are not filled in here.
              </>
            }
          />
          {typedIn(config.connectionString) && (
            <Text size="xs" c="var(--mantine-color-yellow-text)">
              This connection string is saved with the workflow and included in exports. If it carries a password,
              save it as a vhost secret and use {'{{secret("NAME")}}'}.
            </Text>
          )}
          <Group grow align="flex-start" gap="sm">
            <TextInput
              label="Table"
              placeholder="e.g. documents"
              value={str(config.table)}
              onChange={(e) => set({ table: e.currentTarget.value })}
              required
              error={errorFor('table')}
            />
            <Select
              label="Distance metric"
              data={PGVECTOR_METRICS}
              value={str(config.metric) || null}
              placeholder="Cosine"
              onChange={(v) => set({ metric: v ?? '' })}
              clearable
              description="The one the table's index uses."
            />
          </Group>
          <Group grow align="flex-start" gap="sm">
            <TextInput
              label="Vector column"
              placeholder={RETRIEVE_DEFAULTS.vectorColumn}
              value={str(config.vectorColumn)}
              onChange={(e) => set({ vectorColumn: e.currentTarget.value })}
            />
            <TextInput
              label="ID column"
              placeholder={RETRIEVE_DEFAULTS.idColumn}
              value={str(config.idColumn)}
              onChange={(e) => set({ idColumn: e.currentTarget.value })}
            />
          </Group>
          <Group grow align="flex-start" gap="sm">
            <TextInput
              label="Content column"
              placeholder="Not returned"
              value={str(config.contentColumn)}
              onChange={(e) => set({ contentColumn: e.currentTarget.value })}
              description="Optional. The text of each document."
            />
            <TextInput
              label="Metadata column"
              placeholder={RETRIEVE_DEFAULTS.metadataColumn}
              value={str(config.metadataColumn)}
              onChange={(e) => set({ metadataColumn: e.currentTarget.value })}
            />
          </Group>
        </>
      )}

      {store === 'pinecone' && (
        <>
          <TextInput
            label="Index host"
            placeholder="https://my-index-abc123.svc.pinecone.io"
            value={str(config.indexHost)}
            onChange={(e) => set({ indexHost: e.currentTarget.value })}
            required
            error={errorFor('indexHost')}
            description={
              <>
                The index&apos;s https URL from the Pinecone console. <Code>{'{{secret("NAME")}}'}</Code> works here.
              </>
            }
          />
          <AIKeyField
            configKey="storeApiKey"
            label="Pinecone API key secret"
            provider={undefined}
            required={false}
            example="PINECONE_API_KEY"
            value={config.storeApiKey}
            onChange={set}
          />
          <TextInput
            label="Namespace"
            placeholder="Default namespace"
            value={str(config.namespace)}
            onChange={(e) => set({ namespace: e.currentTarget.value })}
          />
        </>
      )}

      <Divider label="Query" labelPosition="center" />
      <Input.Wrapper label="Search for">
        <SegmentedControl
          mt={4}
          fullWidth
          value={mode}
          onChange={chooseMode}
          data={[
            { label: 'A template', value: 'template' },
            { label: 'A field', value: 'field' },
          ]}
        />
      </Input.Wrapper>
      {mode === 'template' ? (
        <Textarea
          label="Query template"
          placeholder={'e.g. {{.subject}} {{.body}}'}
          value={str(config.query)}
          onChange={(e) => set({ query: e.currentTarget.value })}
          autosize
          minRows={2}
          error={errorFor('query')}
          description={'Use {{.field}} to insert a field of the record.'}
        />
      ) : (
        <Autocomplete
          label="Query field"
          placeholder="e.g. question"
          data={fieldPaths}
          value={queryField}
          onChange={(v) => set({ queryField: v })}
          error={errorFor('query')}
          description="Its value is searched for as it is."
        />
      )}

      <Divider label="Results" labelPosition="center" />
      <Group grow align="flex-start" gap="sm">
        <NumberInput
          label="Number of documents"
          placeholder={String(RETRIEVE_DEFAULTS.topK)}
          min={1}
          max={RETRIEVE_DEFAULTS.maxTopK}
          allowDecimal={false}
          value={str(config.topK)}
          onChange={(v) => set({ topK: asText(v) })}
          description={`At most ${RETRIEVE_DEFAULTS.maxTopK}.`}
        />
        <NumberInput
          label="Minimum score"
          placeholder="No minimum"
          step={0.05}
          decimalScale={4}
          value={str(config.minScore)}
          onChange={(v) => set({ minScore: asText(v) })}
          description="Documents scoring lower are left out. Higher is closer."
        />
      </Group>
      <TextInput
        label="Target field"
        placeholder={RETRIEVE_DEFAULTS.targetField}
        value={str(config.targetField)}
        onChange={(e) => set({ targetField: e.currentTarget.value })}
        description={`Receives the list of {id, score, content, metadata}. Empty means ${RETRIEVE_DEFAULTS.targetField}.`}
      />
    </Stack>
  )
}
