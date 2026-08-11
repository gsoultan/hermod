import { 
  IconAdjustments, IconArrowsSplit, IconChecklist, IconCircles, IconCloud, IconCloudUpload, IconDatabase, 
  IconDeviceFloppy, IconEye, IconFileSpreadsheet, IconFilter, IconGitBranch, IconGitMerge, IconHistory, 
  IconMail, IconMessage, IconPlaylist, IconSearch, IconSettingsAutomation, IconShieldLock, 
  IconTerminal2, IconVariable, IconWorld, IconCircleCheck, IconChartBar, IconCode, 
  IconTable, IconBroadcast, IconRefresh, IconLetterCase, IconPercentage, IconTableExport, IconNumbers,
  IconDatabaseExport, IconNote, IconTag, IconBrandDiscord, IconBrandSlack, IconBrandTwitter, 
  IconBrandFacebook, IconBrandInstagram, IconBrandLinkedin, IconBrandTiktok, IconExternalLink, IconLock
} from '@tabler/icons-react';

export const NODE_CATEGORIES = [
  {
    title: 'Common Transformations',
    group: 'transformations',
    items: [
      { type: 'transformation', refId: 'new', label: 'Mapping', subType: 'mapping', icon: IconFilter, color: 'violet', description: 'Map fields and reshape payloads' },
      { type: 'transformation', refId: 'new', label: 'Set Fields', subType: 'set', icon: IconVariable, color: 'violet', description: 'Add or override fields' },
      { type: 'transformation', refId: 'new', label: 'Foreach / Fanout', subType: 'foreach', icon: IconCircles, color: 'violet', description: 'Iterate array items and fan out' },
      { type: 'transformation', refId: 'new', label: 'Filter', subType: 'filter_data', icon: IconEye, color: 'violet', description: 'Keep or drop records by condition' },
      { type: 'transformation', refId: 'new', label: 'Join / Enrich', subType: 'join', icon: IconGitMerge, color: 'violet', description: 'Join with data from state store' },
      { type: 'transformation', refId: 'new', label: 'Data Quality Scorer', subType: 'dq_scorer', icon: IconChecklist, color: 'orange', description: 'Score data completeness and quality' },
      { type: 'transformation', refId: 'new', label: 'Statistical Validation', subType: 'stat_validator', icon: IconChecklist, color: 'orange', description: 'Detect anomalies using drift detection' },
      { type: 'validator', refId: 'new', label: 'Validator', subType: 'validator', icon: IconChecklist, color: 'orange', description: 'Validate required fields and formats' },
      { type: 'transformation', refId: 'new', label: 'Mask Data', subType: 'mask', icon: IconShieldLock, color: 'violet', description: 'Mask or hash sensitive values' },
      { type: 'transformation', refId: 'new', label: 'Rate Limit', subType: 'rate_limit', icon: IconAdjustments, color: 'violet', description: 'Throttle message flow' },
    ]
  },
  {
    title: 'Logic & Flow',
    group: 'transformations',
    items: [
      { type: 'join', refId: 'new', label: 'Stateful Join', subType: 'join', icon: IconGitMerge, color: 'indigo', description: 'Wait and join multiple events by key' },
      { type: 'circuit_breaker', refId: 'new', label: 'Circuit Breaker', subType: 'cb', icon: IconShieldLock, color: 'red', description: 'Stop flow on failure threshold' },
      { type: 'condition', refId: 'new', label: 'Condition (If)', subType: 'condition', icon: IconArrowsSplit, color: 'indigo', description: 'Branch flow by boolean rule' },
      { type: 'router', refId: 'new', label: 'Content Router', subType: 'router', icon: IconArrowsSplit, color: 'indigo', description: 'Route by pattern-based rules' },
      { type: 'switch', refId: 'new', label: 'Switch', subType: 'switch', icon: IconGitBranch, color: 'orange', description: 'Route by multi-case expression' },
      { type: 'foreach', refId: 'new', label: 'Foreach (Fan-out)', subType: 'foreach', icon: IconCircles, color: 'indigo', description: 'Split one message into multiple parallel paths' },
      { type: 'merge', refId: 'new', label: 'Merge', subType: 'merge', icon: IconGitMerge, color: 'cyan', description: 'Join multiple paths' },
      { type: 'wait', refId: 'new', label: 'Wait (Pause)', subType: 'wait', icon: IconHistory, color: 'indigo', description: 'Pause execution for a specific duration (supports long-running)' },
      { type: 'transformation', refId: 'new', label: 'Aggregate', subType: 'aggregate', icon: IconDatabase, color: 'pink', description: 'Group and summarize records' },
      { type: 'stateful', refId: 'new', label: 'Stateful', subType: 'stateful', icon: IconDatabase, color: 'pink', description: 'Store and recall workflow state' },
      { type: 'approval', refId: 'new', label: 'Approval (Human Gate)', subType: 'approval', icon: IconCircleCheck, color: 'green', description: 'Pause execution until approved or rejected' },
    ]
  },
  {
    title: 'Advanced Transformations',
    group: 'transformations',
    items: [
      { type: 'transformation', refId: 'new', label: 'DB Lookup', subType: 'db_lookup', icon: IconSearch, color: 'teal', description: 'Enrich data from a database' },
      { type: 'transformation', refId: 'new', label: 'API Lookup', subType: 'api_lookup', icon: IconCloud, color: 'teal', description: 'Fetch and merge from HTTP APIs' },
      { type: 'transformation', refId: 'new', label: 'AI Enrichment', subType: 'ai_enrichment', icon: IconSettingsAutomation, color: 'teal', description: 'Enrich data using LLMs (OpenAI, Ollama)' },
      { type: 'transformation', refId: 'new', label: 'AI Mapper', subType: 'ai_mapper', icon: IconSettingsAutomation, color: 'teal', description: 'Map unstructured data to schema using AI' },
      { type: 'transformation', refId: 'new', label: 'Pipeline', subType: 'pipeline', icon: IconPlaylist, color: 'teal', description: 'Compose multiple steps' },
      { type: 'transformation', refId: 'new', label: 'Lua Script', subType: 'lua', icon: IconCode, color: 'teal', description: 'Custom logic with Lua' },
      { type: 'transformation', refId: 'new', label: 'WASM Transform', subType: 'wasm', icon: IconTerminal2, color: 'teal', description: 'Run high-performance WebAssembly' },
      { type: 'transformation', refId: 'new', label: 'Advanced', subType: 'advanced', icon: IconCode, color: 'teal', description: 'Power-user transforms' },
      { type: 'transformation', refId: 'new', label: 'Pivot', subType: 'pivot', icon: IconTable, color: 'teal', description: 'Rotate rows into columns' },
      { type: 'transformation', refId: 'new', label: 'Multicast', subType: 'multicast', icon: IconBroadcast, color: 'teal', description: 'Clone message to multiple branches' },
    ]
  },
  {
    title: 'Data Flow (SSIS)',
    group: 'transformations',
    items: [
      { type: 'transformation', refId: 'new', label: 'Data Conversion', subType: 'data_conversion', icon: IconRefresh, color: 'blue', description: 'Explicit type casting between types' },
      { type: 'transformation', refId: 'new', label: 'Character Map', subType: 'char_map', icon: IconLetterCase, color: 'blue', description: 'String normalization (Upper, Lower, Trim)' },
      { type: 'transformation', refId: 'new', label: 'Audit', subType: 'audit', icon: IconHistory, color: 'blue', description: 'Inject execution metadata' },
      { type: 'transformation', refId: 'new', label: 'Sampling', subType: 'sampling', icon: IconPercentage, color: 'blue', description: 'Percentage or row-based sampling' },
      { type: 'transformation', refId: 'new', label: 'Fuzzy Lookup', subType: 'fuzzy_lookup', icon: IconSearch, color: 'blue', description: 'Approximate string matching' },
      { type: 'transformation', refId: 'new', label: 'Term Extraction', subType: 'term_extraction', icon: IconTag, color: 'blue', description: 'Extract keywords from text' },
      { type: 'transformation', refId: 'new', label: 'Unpivot', subType: 'unpivot', icon: IconTableExport, color: 'blue', description: 'Rotate columns to rows' },
      { type: 'transformation', refId: 'new', label: 'Row Count', subType: 'row_count', icon: IconNumbers, color: 'blue', description: 'Track message counts in state' },
      { type: 'transformation', refId: 'new', label: 'Execute SQL', subType: 'execute_sql', icon: IconDatabaseExport, color: 'blue', description: 'Execute action SQL per record' },
      { type: 'transformation', refId: 'new', label: 'SCD', subType: 'scd', icon: IconHistory, color: 'blue', description: 'Slowly Changing Dimension logic' },
    ]
  },
  {
    title: 'Databases',
    group: 'sources',
    items: [
      { type: 'source', refId: 'new', label: 'PostgreSQL', subType: 'postgres', icon: IconDatabase, color: 'blue', description: 'CDC & query capture from Postgres' },
      { type: 'source', refId: 'new', label: 'MySQL', subType: 'mysql', icon: IconDatabase, color: 'blue', description: 'CDC from MySQL binlog' },
      { type: 'source', refId: 'new', label: 'MariaDB', subType: 'mariadb', icon: IconDatabase, color: 'blue', description: 'CDC from MariaDB binlog' },
      { type: 'source', refId: 'new', label: 'SQL Server', subType: 'mssql', icon: IconDatabase, color: 'blue', description: 'CDC/read from SQL Server' },
      { type: 'source', refId: 'new', label: 'Oracle', subType: 'oracle', icon: IconDatabase, color: 'blue', description: 'CDC/read from Oracle' },
      { type: 'source', refId: 'new', label: 'MongoDB', subType: 'mongodb', icon: IconDatabase, color: 'blue', description: 'Change streams from MongoDB' },
      { type: 'source', refId: 'new', label: 'Cassandra', subType: 'cassandra', icon: IconDatabase, color: 'blue', description: 'Read from Cassandra' },
      { type: 'source', refId: 'new', label: 'SQLite', subType: 'sqlite', icon: IconDatabase, color: 'blue', description: 'Local SQLite file as source' },
      { type: 'source', refId: 'new', label: 'ClickHouse', subType: 'clickhouse', icon: IconDatabase, color: 'blue', description: 'Ingest from ClickHouse' },
      { type: 'source', refId: 'new', label: 'YugabyteDB', subType: 'yugabyte', icon: IconDatabase, color: 'blue', description: 'CDC/read from Yugabyte' },
      { type: 'source', refId: 'new', label: 'IBM DB2', subType: 'db2', icon: IconDatabase, color: 'blue', description: 'CDC/read from DB2' },
      { type: 'source', refId: 'new', label: 'ScyllaDB', subType: 'scylladb', icon: IconDatabase, color: 'blue', description: 'Read from ScyllaDB' },
      { type: 'source', refId: 'new', label: 'Event Store', subType: 'eventstore', icon: IconDatabase, color: 'blue', description: 'Replay events for projections' },
      { type: 'source', refId: 'new', label: 'Batch SQL', subType: 'batch_sql', icon: IconDatabase, color: 'blue', description: 'Scheduled full-table syncs' },
    ]
  },
  {
    title: 'Messaging & Streams',
    group: 'sources',
    items: [
      { type: 'source', refId: 'new', label: 'Kafka', subType: 'kafka', icon: IconCircles, color: 'indigo', description: 'Consume from Kafka topics' },
      { type: 'source', refId: 'new', label: 'NATS', subType: 'nats', icon: IconCircles, color: 'indigo', description: 'Consume from NATS JetStream' },
      { type: 'source', refId: 'new', label: 'RabbitMQ Stream', subType: 'rabbitmq', icon: IconCircles, color: 'indigo', description: 'Consume from RMQ Stream' },
      { type: 'source', refId: 'new', label: 'RabbitMQ Queue', subType: 'rabbitmq_queue', icon: IconCircles, color: 'indigo', description: 'Consume from AMQP queues' },
      { type: 'source', refId: 'new', label: 'Redis Stream', subType: 'redis', icon: IconCircles, color: 'indigo', description: 'Consume from Redis Streams' },
      { type: 'source', refId: 'new', label: 'WebSocket (Client)', subType: 'websocket', icon: IconBroadcast, color: 'indigo', description: 'Dial WS feed and ingest frames' },
    ]
  },
  {
    title: 'Social Media',
    group: 'sources',
    items: [
      { type: 'source', refId: 'new', label: 'Discord', subType: 'discord', icon: IconBrandDiscord, color: 'indigo', description: 'Poll messages from a Discord channel' },
      { type: 'source', refId: 'new', label: 'Slack', subType: 'slack', icon: IconBrandSlack, color: 'indigo', description: 'Poll messages from a Slack channel' },
      { type: 'source', refId: 'new', label: 'Twitter (X)', subType: 'twitter', icon: IconBrandTwitter, color: 'indigo', description: 'Poll tweets by search query' },
      { type: 'source', refId: 'new', label: 'Facebook', subType: 'facebook', icon: IconBrandFacebook, color: 'indigo', description: 'Poll posts from a Facebook Page' },
      { type: 'source', refId: 'new', label: 'Instagram', subType: 'instagram', icon: IconBrandInstagram, color: 'indigo', description: 'Poll media from Instagram Business account' },
      { type: 'source', refId: 'new', label: 'LinkedIn', subType: 'linkedin', icon: IconBrandLinkedin, color: 'indigo', description: 'Poll UGC posts from LinkedIn' },
      { type: 'source', refId: 'new', label: 'TikTok', subType: 'tiktok', icon: IconBrandTiktok, color: 'indigo', description: 'Poll videos from TikTok' },
    ]
  },
  {
    title: 'Enterprise',
    group: 'sources',
    items: [
      { type: 'source', refId: 'new', label: 'SAP OData', subType: 'sap', icon: IconCloud, color: 'violet', description: 'Poll data from SAP S/4HANA or ECC' },
      { type: 'source', refId: 'new', label: 'Dynamics 365', subType: 'dynamics365', icon: IconCloud, color: 'violet', description: 'Dataverse OData Web API' },
      { type: 'source', refId: 'new', label: 'Mainframe', subType: 'mainframe', icon: IconDatabase, color: 'violet', description: 'CDC for DB2 or VSAM bridge' },
    ]
  },
  {
    title: 'Others',
    group: 'sources',
    items: [
      { type: 'source', refId: 'new', label: 'Webhook', subType: 'webhook', icon: IconWorld, color: 'cyan', description: 'Receive HTTP POST events' },
      { type: 'source', refId: 'new', label: 'Form Submission', subType: 'form', icon: IconWorld, color: 'cyan', description: 'Accept form submissions via HTTP' },
      { type: 'source', refId: 'new', label: 'Cron / Schedule', subType: 'cron', icon: IconSettingsAutomation, color: 'cyan', description: 'Emit on a schedule' },
      { type: 'source', refId: 'new', label: 'CSV / File', subType: 'file', icon: IconFileSpreadsheet, color: 'cyan', description: 'Read rows from CSV/TSV and files' },
      { type: 'source', refId: 'new', label: 'Excel (.xlsx)', subType: 'excel', icon: IconFileSpreadsheet, color: 'cyan', description: 'Stream rows from Excel workbooks' },
      { type: 'source', refId: 'new', label: 'Google Sheets', subType: 'googlesheets', icon: IconFileSpreadsheet, color: 'cyan', description: 'Poll a Google Sheet' },
      { type: 'source', refId: 'new', label: 'Google Analytics', subType: 'googleanalytics', icon: IconChartBar, color: 'cyan', description: 'Fetch reports from GA4' },
      { type: 'source', refId: 'new', label: 'Firebase', subType: 'firebase', icon: IconDatabase, color: 'cyan', description: 'Poll Firestore collections' },
      { type: 'source', refId: 'new', label: 'HTTP Polling', subType: 'http', icon: IconCloud, color: 'cyan', description: 'Poll REST/OData APIs' },
      { type: 'source', refId: 'new', label: 'GraphQL', subType: 'graphql', icon: IconWorld, color: 'cyan', description: 'Receive GraphQL queries/mutations' },
      { type: 'source', refId: 'new', label: 'gRPC', subType: 'grpc', icon: IconTerminal2, color: 'cyan', description: 'Receive gRPC Publish calls' },
      { type: 'source', refId: 'new', label: 'WebSocket (Server)', subType: 'webhook', icon: IconBroadcast, color: 'cyan', description: 'Accept WS frames at /api/ws/in/{path}' },
    ]
  },
  {
    title: 'Databases',
    group: 'sinks',
    items: [
      { type: 'sink', refId: 'new', label: 'PostgreSQL', subType: 'postgres', icon: IconDatabase, color: 'green', description: 'Write rows to Postgres' },
      { type: 'sink', refId: 'new', label: 'MySQL', subType: 'mysql', icon: IconDatabase, color: 'green', description: 'Write rows to MySQL' },
      { type: 'sink', refId: 'new', label: 'MariaDB', subType: 'mariadb', icon: IconDatabase, color: 'green', description: 'Write rows to MariaDB' },
      { type: 'sink', refId: 'new', label: 'SQL Server', subType: 'mssql', icon: IconDatabase, color: 'green', description: 'Write rows to SQL Server' },
      { type: 'sink', refId: 'new', label: 'Oracle', subType: 'oracle', icon: IconDatabase, color: 'green', description: 'Write rows to Oracle' },
      { type: 'sink', refId: 'new', label: 'MongoDB', subType: 'mongodb', icon: IconDatabase, color: 'green', description: 'Insert docs into MongoDB' },
      { type: 'sink', refId: 'new', label: 'SQLite', subType: 'sqlite', icon: IconDatabase, color: 'green', description: 'Write rows to SQLite' },
      { type: 'sink', refId: 'new', label: 'ClickHouse', subType: 'clickhouse', icon: IconDatabase, color: 'green', description: 'Insert into ClickHouse' },
      { type: 'sink', refId: 'new', label: 'Salesforce', subType: 'salesforce', icon: IconCloudUpload, color: 'green', description: 'Bulk API 2.0 integration' },
      { type: 'sink', refId: 'new', label: 'ServiceNow', subType: 'servicenow', icon: IconExternalLink, color: 'green', description: 'Table API integration' },
      { type: 'sink', refId: 'new', label: 'Elasticsearch', subType: 'elasticsearch', icon: IconSearch, color: 'green', description: 'Index documents into Elasticsearch' },
      { type: 'sink', refId: 'new', label: 'YugabyteDB', subType: 'yugabyte', icon: IconDatabase, color: 'green', description: 'Write rows to Yugabyte' },
      { type: 'sink', refId: 'new', label: 'Snowflake', subType: 'snowflake', icon: IconDatabase, color: 'green', description: 'High-performance cloud data warehouse' },
      { type: 'sink', refId: 'new', label: 'SAP', subType: 'sap', icon: IconCloud, color: 'green', description: 'Write to SAP via OData/BAPI/IDOC' },
      { type: 'sink', refId: 'new', label: 'Dynamics 365', subType: 'dynamics365', icon: IconCloud, color: 'green', description: 'Write to Dataverse via Web API' },
      { type: 'sink', refId: 'new', label: 'Event Store', subType: 'eventstore', icon: IconDatabase, color: 'green', description: 'Unified Event Log (Event Sourcing)' },
    ]
  },
  {
    title: 'Vector Databases',
    group: 'sinks',
    items: [
      { type: 'sink', refId: 'new', label: 'Pgvector', subType: 'pgvector', icon: IconDatabase, color: 'grape', description: 'Store embeddings in Postgres' },
      { type: 'sink', refId: 'new', label: 'Pinecone', subType: 'pinecone', icon: IconCloud, color: 'grape', description: 'Managed vector database' },
      { type: 'sink', refId: 'new', label: 'Milvus', subType: 'milvus', icon: IconDatabase, color: 'grape', description: 'Open-source vector database' },
    ]
  },
  {
    title: 'Messaging & Streams',
    group: 'sinks',
    items: [
      { type: 'sink', refId: 'new', label: 'Kafka', subType: 'kafka', icon: IconCircles, color: 'teal', description: 'Publish to Kafka topics' },
      { type: 'sink', refId: 'new', label: 'NATS', subType: 'nats', icon: IconCircles, color: 'teal', description: 'Publish to NATS JetStream' },
      { type: 'sink', refId: 'new', label: 'RabbitMQ Stream', subType: 'rabbitmq', icon: IconCircles, color: 'teal', description: 'Publish to RMQ Stream' },
      { type: 'sink', refId: 'new', label: 'RabbitMQ Queue', subType: 'rabbitmq_queue', icon: IconCircles, color: 'teal', description: 'Publish to AMQP queues' },
      { type: 'sink', refId: 'new', label: 'Redis Stream', subType: 'redis', icon: IconCircles, color: 'teal', description: 'Publish to Redis Streams' },
      { type: 'sink', refId: 'new', label: 'Google Pub/Sub', subType: 'pubsub', icon: IconCircles, color: 'teal', description: 'Publish to GCP Pub/Sub' },
      { type: 'sink', refId: 'new', label: 'AWS Kinesis', subType: 'kinesis', icon: IconCircles, color: 'teal', description: 'Publish to AWS Kinesis' },
      { type: 'sink', refId: 'new', label: 'Apache Pulsar', subType: 'pulsar', icon: IconCircles, color: 'teal', description: 'Publish to Pulsar topics' },
    ]
  },
  {
    title: 'Notifications & Others',
    group: 'sinks',
    items: [
      { type: 'sink', refId: 'new', label: 'API / Webhook', subType: 'http', icon: IconCloudUpload, color: 'lime', description: 'POST events to HTTP endpoints' },
      { type: 'sink', refId: 'new', label: 'SMTP (Email)', subType: 'smtp', icon: IconMail, color: 'lime', description: 'Send messages via email' },
      { type: 'sink', refId: 'new', label: 'Telegram', subType: 'telegram', icon: IconMessage, color: 'lime', description: 'Send messages to Telegram' },
      { type: 'sink', refId: 'new', label: 'Firebase (FCM)', subType: 'fcm', icon: IconMessage, color: 'lime', description: 'Push notifications via FCM' },
      { type: 'sink', refId: 'new', label: 'File', subType: 'file', icon: IconDeviceFloppy, color: 'lime', description: 'Append to a local file' },
      { type: 'sink', refId: 'new', label: 'Stdout', subType: 'stdout', icon: IconTerminal2, color: 'lime', description: 'Print to console' },
      { type: 'sink', refId: 'new', label: 'Server-Sent Events (SSE)', subType: 'sse', icon: IconBroadcast, color: 'lime', description: 'Stream events to web clients in real-time' },
      { type: 'sink', refId: 'new', label: 'WebSocket', subType: 'websocket', icon: IconBroadcast, color: 'lime', description: 'Publish frames to a WS server' },
      { type: 'sink', refId: 'new', label: 'Google Sheets', subType: 'googlesheets', icon: IconFileSpreadsheet, color: 'lime', description: 'Write to Google Sheets' },
      { type: 'sink', refId: 'new', label: 'AWS S3', subType: 's3', icon: IconCloud, color: 'lime', description: 'Store objects in S3' },
      { type: 'sink', refId: 'new', label: 'AWS S3 Parquet', subType: 's3-parquet', icon: IconCloud, color: 'lime', description: 'Store Parquet files in S3' },
      { type: 'sink', refId: 'new', label: 'FTP / FTPS', subType: 'ftp', icon: IconCloud, color: 'lime', description: 'Upload files via FTP/FTPS' },
    ]
  },
  {
    title: 'Social Media',
    group: 'sinks',
    items: [
      { type: 'sink', refId: 'new', label: 'Discord', subType: 'discord', icon: IconBrandDiscord, color: 'pink', description: 'Post messages to Discord (Webhook or Bot)' },
      { type: 'sink', refId: 'new', label: 'Slack', subType: 'slack', icon: IconBrandSlack, color: 'pink', description: 'Post messages to Slack (Webhook or Bot)' },
      { type: 'sink', refId: 'new', label: 'Twitter (X)', subType: 'twitter', icon: IconBrandTwitter, color: 'pink', description: 'Post tweets' },
      { type: 'sink', refId: 'new', label: 'Facebook', subType: 'facebook', icon: IconBrandFacebook, color: 'pink', description: 'Post to Facebook Page feed' },
      { type: 'sink', refId: 'new', label: 'Instagram', subType: 'instagram', icon: IconBrandInstagram, color: 'pink', description: 'Publish media to Instagram' },
      { type: 'sink', refId: 'new', label: 'LinkedIn', subType: 'linkedin', icon: IconBrandLinkedin, color: 'pink', description: 'Create UGC posts on LinkedIn' },
      { type: 'sink', refId: 'new', label: 'TikTok', subType: 'tiktok', icon: IconBrandTiktok, color: 'pink', description: 'Publish videos to TikTok' },
    ]
  },
  {
    title: 'Groups & Logic',
    group: 'sinks',
    items: [
      { type: 'sink', refId: 'new', label: 'Failover Group', subType: 'failover', icon: IconArrowsSplit, color: 'orange', description: 'Primary/Secondary failover logic' },
      { type: 'sink', refId: 'new', label: 'Transactional Group (2PC)', subType: 'txgroup', icon: IconLock, color: 'orange', description: 'Write to several sinks in one transaction — all apply or none' },
    ]
  },
  {
    title: 'Utilities',
    group: 'transformations',
    items: [
      { type: 'note', refId: 'new', label: 'Note', subType: 'note', icon: IconNote, color: 'yellow', description: 'Add annotations in canvas' },
    ]
  }
];
