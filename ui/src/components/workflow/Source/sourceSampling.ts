import type { Source } from '@/types';

/**
 * Sources whose preview is read-only by design. Sampling these never advances a
 * watermark, consumes a queue message, or deletes a file, so the UI can safely
 * advertise a "non-destructive preview".
 */
const NON_DESTRUCTIVE_TYPES = new Set<string>([
  // Databases are sampled with a bounded SELECT and never mutate CDC state.
  'postgres', 'mysql', 'mariadb', 'mssql', 'oracle', 'mongodb', 'cassandra',
  'sqlite', 'clickhouse', 'yugabyte', 'db2', 'scylladb', 'eventstore',
  'batch_sql',
  // File backends read the first matching file without removing or marking it.
  'file', 'excel', 'googlesheets',
  // Pull-based APIs perform a single read-only request.
  'http', 'graphql', 'grpc', 'sap', 'dynamics365',
]);

/**
 * isNonDestructiveSample reports whether sampling the given source type is
 * guaranteed not to consume or skip real data during a later run.
 */
export function isNonDestructiveSample(sourceType: string): boolean {
  return NON_DESTRUCTIVE_TYPES.has(sourceType);
}

const DATABASE_TYPES = new Set<string>([
  'postgres', 'mysql', 'mssql', 'oracle', 'mongodb', 'yugabyte', 'mariadb',
  'db2', 'cassandra', 'scylladb', 'clickhouse', 'sqlite', 'eventstore',
]);

const MESSAGING_TYPES = new Set<string>([
  'kafka', 'nats', 'rabbitmq', 'rabbitmq_queue', 'redis', 'mqtt',
]);

function firstNonEmpty(config: Record<string, any> | undefined, keys: string[]): boolean {
  if (!config) return false;
  return keys.some((k) => {
    const v = config[k];
    return typeof v === 'string' ? v.trim().length > 0 : Boolean(v);
  });
}

export interface SampleValidation {
  /** Whether the source has enough information to attempt a sample/test. */
  valid: boolean;
  /** Human-readable, field-oriented reasons why sampling is currently blocked. */
  issues: string[];
}

/**
 * validateSourceForSampling performs a lightweight, conservative pre-flight
 * check so the UI can disable "Fetch Sample"/"Run Simulation" and explain why,
 * rather than letting users fire a request that is guaranteed to fail.
 *
 * It intentionally errs on the side of allowing the request when a source type
 * is unknown, so new source types are never blocked by stale rules.
 */
export function validateSourceForSampling(source: Source): SampleValidation {
  const issues: string[] = [];
  const type = source?.type || '';
  const config = (source?.config || {}) as Record<string, any>;

  if (!source?.name || !String(source.name).trim()) {
    issues.push('Give the source a name before previewing.');
  }

  if (DATABASE_TYPES.has(type)) {
    const hasConn = firstNonEmpty(config, ['connection_string', 'host', 'path']);
    if (!hasConn) {
      issues.push('Provide a connection string, host, or file path to connect.');
    }
  } else if (MESSAGING_TYPES.has(type)) {
    if (!firstNonEmpty(config, ['brokers', 'url', 'addr', 'address', 'servers', 'broker_url'])) {
      issues.push('Provide the broker/server address.');
    }
    if (!firstNonEmpty(config, ['topic', 'topics', 'subject', 'queue', 'stream', 'channel'])) {
      issues.push('Provide a topic, queue, or subject to read from.');
    }
  } else if (type === 'file') {
    if (!firstNonEmpty(config, [
      'base_path', 'file_path', 'local_path', 'pattern',
      's3_bucket', 's3_key', 'url', 'ftp_host',
    ])) {
      issues.push('Provide a file path, pattern, or remote location.');
    }
  } else if (type === 'excel') {
    if (!firstNonEmpty(config, ['file_path', 'base_path', 's3_bucket', 's3_key'])) {
      issues.push('Upload or point to an Excel file.');
    }
  } else if (['http', 'graphql', 'grpc', 'websocket'].includes(type)) {
    if (!firstNonEmpty(config, ['url', 'endpoint', 'address', 'addr'])) {
      issues.push('Provide the endpoint URL.');
    }
  } else if (type === 'batch_sql') {
    if (!firstNonEmpty(config, ['source_id', 'connection_string', 'host'])) {
      issues.push('Select the database connection to query.');
    }
    if (!firstNonEmpty(config, ['query', 'table'])) {
      issues.push('Provide the SQL query or table to read.');
    }
  }

  return { valid: issues.length === 0, issues };
}

interface ErrorRule {
  match: RegExp;
  message: (sourceType: string) => string;
}

/**
 * ERROR_RULES maps common low-level/Go error fragments to actionable,
 * human-readable guidance. Order matters: the first match wins.
 */
const ERROR_RULES: ErrorRule[] = [
  {
    match: /no files found|no files matched|pattern/i,
    message: () => 'No files matched your path or pattern. Check the directory and glob (e.g. "*.csv").',
  },
  {
    match: /connection refused|dial tcp|no such host|i\/o timeout|connect: /i,
    message: () => 'Could not reach the server. Verify the host, port, and that the service is running.',
  },
  {
    match: /password authentication failed|authentication failed|access denied|permission denied|unauthorized|401|403/i,
    message: () => 'Authentication failed. Check the username, password, or access credentials.',
  },
  {
    match: /database .* does not exist|unknown database|no such database/i,
    message: () => 'The database was not found. Verify the database name.',
  },
  {
    match: /relation .* does not exist|table .* doesn'?t exist|no such table|unknown table/i,
    message: () => 'The selected table was not found. Pick an existing table to sample.',
  },
  {
    match: /context deadline exceeded|timeout|timed out|no message within/i,
    message: (t) =>
      MESSAGING_TYPES.has(t)
        ? 'No message arrived within the timeout. Ensure the topic/queue has recent data, then retry.'
        : 'The request timed out. The source may be slow or unreachable — try again.',
  },
  {
    match: /sampling is not supported/i,
    message: () => 'This source configuration cannot be previewed. Run a test connection instead.',
  },
  {
    match: /tls|x509|certificate/i,
    message: () => 'A TLS/certificate error occurred. Check SSL settings and certificates.',
  },
];

/**
 * humanizeSampleError converts a raw backend error into a concise, actionable
 * message. The original text is preserved when no rule matches so power users
 * can still diagnose unusual failures.
 */
export function humanizeSampleError(raw: string | null | undefined, sourceType = ''): string {
  const text = (raw || '').trim();
  if (!text) return 'Failed to fetch sample data. Please verify the configuration and try again.';
  for (const rule of ERROR_RULES) {
    if (rule.match.test(text)) {
      return rule.message(sourceType);
    }
  }
  return text;
}
