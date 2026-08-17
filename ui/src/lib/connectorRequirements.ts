/**
 * What each connector type minimally needs before its connection step can be
 * left.
 *
 * The wizards used to gate only the Basics step: Connection advanced with
 * every field blank and the user found out at submit, when the step that was
 * wrong was no longer on screen. This module is the single place that says
 * "for this type, these fields", in the user's words — the Next button, the
 * tooltip that explains it, and any inline messages all read from here, so
 * they cannot disagree.
 *
 * A type not listed requires nothing at the connection step. That is the
 * right default: a missing entry degrades to the old behaviour (find out at
 * submit) rather than to a step nobody can leave.
 */

export interface RequiredField {
  /** Config key, as the factory reads it. */
  key: string;
  /** Human name shown in "Required: …" messages. */
  label: string;
  /** Real-looking example shown as the field placeholder. */
  example: string;
}

const hostPort = (port: string): RequiredField[] => [
  { key: 'host', label: 'Host', example: 'db.example.com' },
  { key: 'port', label: 'Port', example: port },
];

const SOURCE_REQUIREMENTS: Record<string, RequiredField[]> = {
  postgres: hostPort('5432'),
  mysql: hostPort('3306'),
  mariadb: hostPort('3306'),
  mssql: hostPort('1433'),
  oracle: hostPort('1521'),
  clickhouse: hostPort('9000'),
  yugabyte: hostPort('5433'),
  db2: hostPort('50000'),
  sqlite: [{ key: 'path', label: 'Database file path', example: 'hermod.db' }],
  cassandra: [{ key: 'hosts', label: 'Hosts', example: 'node1:9042, node2:9042' }],
  scylladb: [{ key: 'hosts', label: 'Hosts', example: 'node1:9042, node2:9042' }],
  mongodb: [
    { key: 'database', label: 'Database', example: 'app' },
    { key: 'collection', label: 'Collection', example: 'orders' },
  ],
  kafka: [{ key: 'brokers', label: 'Brokers', example: 'broker1:9092, broker2:9092' }],
  nats: [{ key: 'url', label: 'Server URL', example: 'nats://nats.example.com:4222' }],
  rabbitmq: [{ key: 'url', label: 'Server URL', example: 'amqp://user:pass@rabbit.example.com:5672/' }],
  rabbitmq_queue: [
    { key: 'url', label: 'Server URL', example: 'amqp://user:pass@rabbit.example.com:5672/' },
    { key: 'queue', label: 'Queue', example: 'orders' },
  ],
  redis: [{ key: 'addr', label: 'Address', example: 'redis.example.com:6379' }],
  mqtt: [{ key: 'broker', label: 'Broker URL', example: 'tcp://mqtt.example.com:1883' }],
  websocket: [{ key: 'url', label: 'WebSocket URL', example: 'wss://feed.example.com/stream' }],
  http: [{ key: 'url', label: 'URL to poll', example: 'https://api.example.com/changes' }],
  graphql: [{ key: 'url', label: 'GraphQL endpoint', example: 'https://api.example.com/graphql' }],
  webhook: [{ key: 'path', label: 'Webhook path', example: '/api/webhooks/my-source' }],
  form: [{ key: 'path', label: 'Form path', example: '/api/forms/my-form' }],
  grpc: [{ key: 'path', label: 'gRPC path', example: '/grpc/my-source' }],
  cron: [{ key: 'schedule', label: 'Cron schedule', example: '*/5 * * * *' }],
  excel: [{ key: 'pattern', label: 'File pattern', example: 'reports/*.xlsx' }],
  batch_sql: [
    { key: 'cron', label: 'Schedule', example: '0 * * * *' },
    { key: 'queries', label: 'SQL query', example: "SELECT * FROM t WHERE id > '{{.last_value}}'" },
  ],
};

const SINK_REQUIREMENTS: Record<string, RequiredField[]> = {
  postgres: hostPort('5432'),
  mysql: hostPort('3306'),
  mariadb: hostPort('3306'),
  mssql: hostPort('1433'),
  oracle: hostPort('1521'),
  clickhouse: hostPort('9000'),
  yugabyte: hostPort('5433'),
  cassandra: [{ key: 'hosts', label: 'Hosts', example: 'node1:9042, node2:9042' }],
  mongodb: [
    { key: 'database', label: 'Database', example: 'app' },
    { key: 'collection', label: 'Collection', example: 'orders' },
  ],
  kafka: [
    { key: 'brokers', label: 'Brokers', example: 'broker1:9092' },
    { key: 'topic', label: 'Topic', example: 'hermod.events' },
  ],
  redis: [{ key: 'addr', label: 'Address', example: 'redis.example.com:6379' }],
  rabbitmq: [{ key: 'url', label: 'Server URL', example: 'amqp://user:pass@rabbit.example.com:5672/' }],
  elasticsearch: [{ key: 'url', label: 'Server URL', example: 'https://es.example.com:9200' }],
  http: [{ key: 'url', label: 'Destination URL', example: 'https://api.example.com/ingest' }],
  websocket: [{ key: 'url', label: 'WebSocket URL', example: 'wss://receiver.example.com/in' }],
  s3: [
    { key: 'bucket', label: 'Bucket', example: 'my-data-lake' },
    { key: 'region', label: 'Region', example: 'us-east-1' },
  ],
  s3parquet: [
    { key: 'bucket', label: 'Bucket', example: 'my-data-lake' },
    { key: 'region', label: 'Region', example: 'us-east-1' },
  ],
  smtp: [
    { key: 'host', label: 'SMTP host', example: 'smtp.example.com' },
    { key: 'to', label: 'Recipient', example: 'ops@example.com' },
  ],
  snowflake: [{ key: 'dsn', label: 'DSN', example: 'user:pass@account/db/schema?warehouse=wh' }],
};

/**
 * The connection-step fields still missing for a connector, in the user's
 * words. Empty means the step may be left.
 */
export function missingConnectionFields(
  kind: 'source' | 'sink',
  type: string,
  config: Record<string, unknown> | undefined,
): string[] {
  const reqs = (kind === 'source' ? SOURCE_REQUIREMENTS : SINK_REQUIREMENTS)[type] ?? [];
  const cfg = config ?? {};
  return reqs
    .filter((f) => {
      const v = cfg[f.key];
      return v === undefined || v === null || String(v).trim() === '';
    })
    .map((f) => f.label);
}

/**
 * MongoDB's full URI substitutes for its individual fields; a database-family
 * URL paste fills host/port. Either way, a config that carries a `uri` or a
 * recognised `connection_string` satisfies host-shaped requirements.
 */
export function missingConnectionFieldsWithUri(
  kind: 'source' | 'sink',
  type: string,
  config: Record<string, unknown> | undefined,
): string[] {
  const cfg = config ?? {};
  const uri = String(cfg.uri ?? cfg.connection_string ?? '').trim();
  if (uri !== '') return [];
  return missingConnectionFields(kind, type, cfg);
}

/** The example placeholder for a required field, for use by the form. */
export function exampleFor(kind: 'source' | 'sink', type: string, key: string): string | undefined {
  const reqs = (kind === 'source' ? SOURCE_REQUIREMENTS : SINK_REQUIREMENTS)[type] ?? [];
  return reqs.find((f) => f.key === key)?.example;
}
