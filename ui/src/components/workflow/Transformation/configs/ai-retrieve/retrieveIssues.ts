import { isPlaintextKey } from '../ai/aiProviders'

export interface NodeIssue {
  severity: 'error' | 'warning'
  message: string
  /** The config key the issue is about, so the form can show it there. */
  field?: string
}

/** The vector stores ai_retrieve opens (retrieve.go openStore). */
export const RETRIEVE_STORES = ['pgvector', 'pinecone'] as const
export type RetrieveStore = (typeof RETRIEVE_STORES)[number]

/** pgvector distance metrics (pgvector.go distanceOps), compared as written. */
export const PGVECTOR_METRICS = [
  { value: 'cosine', label: 'Cosine' },
  { value: 'l2', label: 'Euclidean (L2)' },
  { value: 'inner_product', label: 'Inner product' },
]

/** Engine defaults (retrieve.go): shown as placeholders, never saved. */
export const RETRIEVE_DEFAULTS = {
  topK: 5,
  maxTopK: 100,
  targetField: 'ai_context',
  timeout: '30s',
  vectorColumn: 'embedding',
  idColumn: 'id',
  metadataColumn: 'metadata',
  metric: 'cosine',
} as const

/** A setting that is absent or blank text (isMissing in the validator). */
export const isMissing = (v: unknown) => v == null || (typeof v === 'string' && v.trim() === '')

/** The store as openStore reads it, or '' when it is not one it opens. */
export function retrieveStore(raw: unknown): RetrieveStore | '' {
  const s = typeof raw === 'string' ? raw.trim().toLowerCase() : ''
  return (RETRIEVE_STORES as readonly string[]).includes(s) ? (s as RetrieveStore) : ''
}

const STORE_KEYS: Record<RetrieveStore, { key: string; message: string }[]> = {
  pgvector: [
    { key: 'connectionString', message: 'pgvector needs a connection string.' },
    { key: 'table', message: 'pgvector needs a table.' },
  ],
  pinecone: [{ key: 'indexHost', message: 'Pinecone needs the index host.' }],
}

/**
 * What workflow validation (workflow_validation_ai.go: aiNodeIssues and
 * retrieveNodeIssues) reports for an ai_retrieve node, in the same order.
 * Without a provider it stops there, as the backend does.
 */
export function retrieveIssues(config: Record<string, unknown>): NodeIssue[] {
  if (isMissing(config.provider)) {
    return [{ severity: 'error', message: 'Choose the AI provider that embeds the query.', field: 'provider' }]
  }
  const issues: NodeIssue[] = []
  const store = retrieveStore(config.store)
  if (!store) {
    issues.push({ severity: 'error', message: 'Choose a vector store (pgvector or pinecone).', field: 'store' })
  } else {
    for (const k of STORE_KEYS[store]) {
      if (isMissing(config[k.key])) issues.push({ severity: 'error', message: k.message, field: k.key })
    }
  }
  if (isMissing(config.query) && isMissing(config.queryField)) {
    issues.push({ severity: 'error', message: 'Write a query or name the field that holds it.', field: 'query' })
  }
  const provider = String(config.provider).trim().toLowerCase()
  if (isPlaintextKey(config.apiKey) && provider !== 'ollama' && provider !== 'openai_compatible') {
    issues.push({
      severity: 'warning',
      message: 'The API key is stored in the workflow. Use a vhost secret instead.',
      field: 'apiKey',
    })
  }
  return issues
}
