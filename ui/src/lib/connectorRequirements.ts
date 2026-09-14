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
  /**
   * Other keys that satisfy this requirement. A connector whose form offers
   * both discrete host fields and a pasted URL has one requirement, not two:
   * BuildConnectionString takes whichever is present.
   */
  aliases?: string[];
  /** Human name shown in "Required: …" messages. */
  label: string;
  /** Real-looking example shown as the field placeholder. */
  example: string;
  /**
   * Whether this field is required *given the rest of the config*. Omitted
   * means always.
   *
   * A connector whose shape changes with a mode setting has requirements that
   * change with it: the metis sink needs a definition key to start a process
   * and a signal name to broadcast one, and demanding both would disable Next
   * for every configuration that is actually valid.
   */
  when?: (config: Record<string, unknown>) => boolean;
}

/**
 * The keys BuildConnectionString accepts as a whole connection string, in its
 * own precedence order (internal/factory/factory.go:1193). Any one of them
 * makes the discrete host/port fields unnecessary.
 */
const URL_KEYS = ['connection_string', 'uri', 'url'];

const hostPort = (port: string): RequiredField[] => [
  { key: 'host', aliases: URL_KEYS, label: 'Host', example: 'db.example.com' },
  { key: 'port', aliases: URL_KEYS, label: 'Port', example: port },
];

/**
 * The metis connectors reach an engine that refuses an empty project rather
 * than widening the call, and authenticate with either a token or a password.
 * `aliases` is what makes those two one requirement rather than two.
 */
const metisConnection: RequiredField[] = [
  { key: 'base_url', label: 'Engine URL', example: 'https://bpm.example.com' },
  { key: 'project_id', label: 'Project ID', example: '0f8b1c2d-…' },
  { key: 'token', aliases: ['username'], label: 'Token or username', example: 'hermod-service' },
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
  metis: metisConnection,
  nats: [{ key: 'url', label: 'Server URL', example: 'nats://nats.example.com:4222' }],
  rabbitmq: [
    { key: 'host', aliases: URL_KEYS, label: 'Host', example: 'rabbit.example.com' },
    { key: 'stream_name', label: 'Stream name', example: 'orders' },
  ],
  rabbitmq_queue: [
    { key: 'host', aliases: URL_KEYS, label: 'Host', example: 'rabbit.example.com' },
    { key: 'queue_name', label: 'Queue name', example: 'orders' },
  ],
  redis: [{ key: 'addr', label: 'Address', example: 'redis.example.com:6379' }],
  mqtt: [
    { key: 'broker_url', aliases: ['url'], label: 'Broker URL', example: 'tcp://mqtt.example.com:1883' },
    { key: 'topics', aliases: ['topic'], label: 'Topics', example: 'sensors/+/temp, devices/+/status' },
  ],
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
  rabbitmq: [
    { key: 'host', aliases: URL_KEYS, label: 'Host', example: 'rabbit.example.com' },
    { key: 'stream_name', label: 'Stream name', example: 'hermod-stream' },
  ],
  rabbitmq_queue: [
    { key: 'host', aliases: URL_KEYS, label: 'Host', example: 'rabbit.example.com' },
    { key: 'queue_name', label: 'Queue name', example: 'hermod-queue' },
  ],
  elasticsearch: [{ key: 'url', label: 'Server URL', example: 'https://es.example.com:9200' }],
  http: [{ key: 'url', label: 'Destination URL', example: 'https://api.example.com/ingest' }],
  websocket: [{ key: 'url', label: 'WebSocket URL', example: 'wss://receiver.example.com/in' }],
  // The three below are gated because their factory case returns an error
  // rather than degrading: sinkmqtt.New refuses a missing broker_url or topic,
  // and the event store calls sql.Open, which cannot work without both. Saving
  // one of these without them produces a sink that only fails when it runs.
  mqtt: [
    { key: 'broker_url', aliases: ['url'], label: 'Broker URL', example: 'tcp://broker.example.com:1883' },
    { key: 'topic', label: 'Topic', example: 'hermod/events' },
  ],
  file: [{ key: 'filename', label: 'Filename', example: '/var/lib/hermod/events.jsonl' }],
  eventstore: [
    { key: 'driver', label: 'Driver', example: 'pgx' },
    { key: 'dsn', label: 'DSN', example: 'postgres://user:pass@host:5432/events' },
  ],
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
  // Every one of these is refused by panmail.New rather than defaulted: the
  // gateway will not guess a provider, and a key or a from address it did not
  // authorise is a send that fails on the first message rather than at save.
  panmail: [
    { key: 'base_url', label: 'Gateway URL', example: 'https://mail.example.com' },
    { key: 'api_key', label: 'API key', example: 'pk_live_…' },
    { key: 'provider_id', label: 'Provider ID', example: '0f8b1c2d-…' },
    { key: 'from', label: 'From address', example: 'noreply@example.com' },
    { key: 'to', label: 'Recipient', example: '{{.email}}' },
  ],
  // The name field follows the action: the factory's metis case returns an
  // error rather than defaulting, so without these you could save a sink that
  // failed only once it ran.
  metis: [
    ...metisConnection,
    {
      key: 'definition_key',
      label: 'Definition key',
      example: 'order-fulfilment',
      when: (c) => (c.action ?? 'start_process') === 'start_process',
    },
    {
      key: 'message_name',
      label: 'Message name',
      example: 'payment-received',
      when: (c) => c.action === 'send_message',
    },
    {
      key: 'signal_name',
      label: 'Signal name',
      example: 'price-list-changed',
      when: (c) => c.action === 'broadcast_signal',
    },
  ],
  snowflake: [{ key: 'connection_string', label: 'Connection String', example: 'user:pass@account/db/schema?warehouse=wh' }],
};

/**
 * The connection-step fields still missing for a connector, in the user's
 * words. Empty means the step may be left.
 *
 * A pasted connection string satisfies the host-shaped fields it replaces and
 * nothing else. It used to satisfy every requirement the connector had: one
 * `uri` opened the gate for a MongoDB source with no database or collection,
 * which the factory reads from their own keys and cannot derive.
 */
export function missingConnectionFields(
  kind: 'source' | 'sink',
  type: string,
  config: Record<string, unknown> | undefined,
): string[] {
  const reqs = (kind === 'source' ? SOURCE_REQUIREMENTS : SINK_REQUIREMENTS)[type] ?? [];
  const cfg = config ?? {};
  const blank = (key: string) => {
    const v = cfg[key];
    return v === undefined || v === null || String(v).trim() === '';
  };
  return reqs
    .filter((f) => f.when?.(cfg) ?? true)
    .filter((f) => blank(f.key) && (f.aliases ?? []).every(blank))
    .map((f) => f.label);
}

/** The example placeholder for a required field, for use by the form. */
export function exampleFor(kind: 'source' | 'sink', type: string, key: string): string | undefined {
  const reqs = (kind === 'source' ? SOURCE_REQUIREMENTS : SINK_REQUIREMENTS)[type] ?? [];
  return reqs.find((f) => f.key === key)?.example;
}
