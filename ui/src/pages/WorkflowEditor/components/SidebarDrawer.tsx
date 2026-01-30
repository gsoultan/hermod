import { 
  Drawer, Tabs, Stack, Group, Paper, Text, ScrollArea, Box, ThemeIcon, UnstyledButton, rem, Title, useMantineColorScheme,
  Select, Checkbox, NumberInput, TextInput, Alert
} from '@mantine/core';
import { useShallow } from 'zustand/react/shallow';
import { 
  IconDatabase, IconCloudUpload, IconWorld, IconSettingsAutomation, 
  IconFileSpreadsheet, IconCircles, IconMail, IconFilter, IconArrowsSplit, 
  IconGitBranch, IconGitMerge, IconNote, IconPlus, IconTable, IconTerminal2,
  IconMessage, IconVariable, IconEye, IconCode,
  IconShieldLock, IconSearch, IconCloud, IconPlaylist, IconDeviceFloppy, IconChecklist,
  IconInfoCircle, IconAdjustments, IconRefresh
} from '@tabler/icons-react';
import { useWorkflowStore } from '../store/useWorkflowStore';

interface SidebarDrawerProps {
  onDragStart: (event: any, nodeType: string, refId: string, label: string, subType: string) => void;
  onAddItem: (type: string, refId: string, label: string, subType: string, Icon: any, color: string) => void;
  sources: any[];
  sinks: any[];
}

export function SidebarDrawer({ 
  onDragStart, onAddItem, sources, sinks
}: SidebarDrawerProps) {
  const { 
    drawerOpened, drawerTab, deadLetterSinkID, dlqThreshold, prioritizeDLQ, dryRun, 
    maxRetries, retryInterval, reconnectInterval, schemaType, schema,
    setDrawerOpened, setDrawerTab, setDeadLetterSinkID, setDlqThreshold, setPrioritizeDLQ, 
    setDryRun, setMaxRetries, setRetryInterval, setReconnectInterval, 
    setSchemaType, setSchema
  } = useWorkflowStore(useShallow(state => ({
    drawerOpened: state.drawerOpened,
    drawerTab: state.drawerTab,
    deadLetterSinkID: state.deadLetterSinkID,
    dlqThreshold: state.dlqThreshold,
    prioritizeDLQ: state.prioritizeDLQ,
    dryRun: state.dryRun,
    maxRetries: state.maxRetries,
    retryInterval: state.retryInterval,
    reconnectInterval: state.reconnectInterval,
    schemaType: state.schemaType,
    schema: state.schema,
    setDrawerOpened: state.setDrawerOpened,
    setDrawerTab: state.setDrawerTab,
    setDeadLetterSinkID: state.setDeadLetterSinkID,
    setDlqThreshold: state.setDlqThreshold,
    setPrioritizeDLQ: state.setPrioritizeDLQ,
    setDryRun: state.setDryRun,
    setMaxRetries: state.setMaxRetries,
    setRetryInterval: state.setRetryInterval,
    setReconnectInterval: state.setReconnectInterval,
    setSchemaType: state.setSchemaType,
    setSchema: state.setSchema
  })));

  const nodeCategories = [
    {
      title: 'Common Transformations',
      items: [
        { type: 'transformation', refId: 'new', label: 'Mapping', subType: 'mapping', icon: IconFilter, color: 'violet', description: 'Map fields and reshape payloads' },
        { type: 'transformation', refId: 'new', label: 'Set Fields', subType: 'set', icon: IconVariable, color: 'violet', description: 'Add or override fields' },
        { type: 'transformation', refId: 'new', label: 'Foreach / Fanout', subType: 'foreach', icon: IconCircles, color: 'violet', description: 'Iterate array items and fan out' },
        { type: 'transformation', refId: 'new', label: 'Filter', subType: 'filter_data', icon: IconEye, color: 'violet', description: 'Keep or drop records by condition' },
        { type: 'validator', refId: 'new', label: 'Validator', subType: 'validator', icon: IconChecklist, color: 'orange', description: 'Validate required fields and formats' },
        { type: 'transformation', refId: 'new', label: 'Mask Data', subType: 'mask', icon: IconShieldLock, color: 'violet', description: 'Mask or hash sensitive values' },
        { type: 'transformation', refId: 'new', label: 'Rate Limit', subType: 'rate_limit', icon: IconAdjustments, color: 'violet', description: 'Throttle message flow' },
      ]
    },
    {
      title: 'Logic & Flow',
      items: [
        { type: 'condition', refId: 'new', label: 'Condition (If)', subType: 'condition', icon: IconArrowsSplit, color: 'indigo', description: 'Branch flow by boolean rule' },
        { type: 'router', refId: 'new', label: 'Content Router', subType: 'router', icon: IconArrowsSplit, color: 'indigo', description: 'Route by pattern-based rules' },
        { type: 'switch', refId: 'new', label: 'Switch', subType: 'switch', icon: IconGitBranch, color: 'orange', description: 'Route by multi-case expression' },
        { type: 'merge', refId: 'new', label: 'Merge', subType: 'merge', icon: IconGitMerge, color: 'cyan', description: 'Join multiple paths' },
        { type: 'transformation', refId: 'new', label: 'Aggregate', subType: 'aggregate', icon: IconDatabase, color: 'pink', description: 'Group and summarize records' },
        { type: 'stateful', refId: 'new', label: 'Stateful', subType: 'stateful', icon: IconDatabase, color: 'pink', description: 'Store and recall workflow state' },
      ]
    },
    {
      title: 'Advanced Transformations',
      items: [
        { type: 'transformation', refId: 'new', label: 'DB Lookup', subType: 'db_lookup', icon: IconSearch, color: 'teal', description: 'Enrich data from a database' },
        { type: 'transformation', refId: 'new', label: 'API Lookup', subType: 'api_lookup', icon: IconCloud, color: 'teal', description: 'Fetch and merge from HTTP APIs' },
        { type: 'transformation', refId: 'new', label: 'Pipeline', subType: 'pipeline', icon: IconPlaylist, color: 'teal', description: 'Compose multiple steps' },
        { type: 'transformation', refId: 'new', label: 'Lua Script', subType: 'lua', icon: IconCode, color: 'teal', description: 'Custom logic with Lua' },
        { type: 'transformation', refId: 'new', label: 'Advanced', subType: 'advanced', icon: IconCode, color: 'teal', description: 'Power-user transforms' },
      ]
    },
    {
      title: 'Utilities',
      items: [
        { type: 'note', refId: 'new', label: 'Note', subType: 'note', icon: IconNote, color: 'yellow', description: 'Add annotations in canvas' },
      ]
    }
  ];

  const { colorScheme } = useMantineColorScheme();
  const isDark = colorScheme === 'dark';

  const selectedDLQSink = sinks.find(s => s.id === deadLetterSinkID);
  const dlqSupportsRecovery = selectedDLQSink && ['postgres', 'mysql', 'mariadb', 'mssql', 'oracle', 'mongodb', 'cassandra', 'sqlite', 'clickhouse', 'yugabyte', 'kafka', 'nats', 'rabbitmq', 'rabbitmq_queue', 'redis', 'pubsub', 'kinesis', 'pulsar', 'elasticsearch'].includes(selectedDLQSink.type);

  const renderDraggableItem = (item: any) => (
    <UnstyledButton
      key={item.label + (item.refId || '') + (item.subType || '')}
      draggable
      onDragStart={(e) => onDragStart(e, item.type, item.refId, item.label, item.subType)}
      onClick={() => onAddItem(item.type, item.refId, item.label, item.subType, item.icon, item.color)}
      style={(theme) => ({
        display: 'block',
        width: '100%',
        padding: '8px 12px',
        borderRadius: theme.radius.md,
        color: isDark ? theme.colors.dark[0] : theme.black,
        transition: 'background-color 0.2s ease, transform 0.1s ease',
        '&:hover': {
          backgroundColor: isDark ? theme.colors.dark[6] : theme.colors.gray[0],
          transform: 'translateX(4px)',
        },
        '&:active': {
          transform: 'translateX(2px)',
        }
      })}
    >
      <Group wrap="nowrap" gap="sm">
        <ThemeIcon variant="light" color={item.color} size="lg" radius="md">
          <item.icon style={{ width: rem(20), height: rem(20) }} />
        </ThemeIcon>
        <Box style={{ flex: 1, overflow: 'hidden' }}>
          <Text size="sm" fw={600} truncate="end">{item.label}</Text>
          <Text size="xs" color="dimmed" truncate="end">
            {item.description || item.subType || item.type}
          </Text>
        </Box>
        <IconPlus size="1.1rem" color="var(--mantine-color-gray-4)" style={{ opacity: 0.6 }} />
      </Group>
    </UnstyledButton>
  );

  return (
    <Drawer
      opened={drawerOpened}
      onClose={() => setDrawerOpened(false)}
      title={
        <Group gap="xs">
          <ThemeIcon variant="light" color="blue" size="md">
            <IconAdjustments size="1.2rem" />
          </ThemeIcon>
          <Title order={4}>Workflow Panel</Title>
        </Group>
      }
      position="right"
      size={380}
      padding="md"
    >
      <Tabs value={drawerTab} onChange={(val) => setDrawerTab(val || "nodes")} variant="pills" radius="md">
        <Tabs.List mb="md" grow style={{ flexWrap: 'nowrap' }}>
          <Tabs.Tab value="nodes" leftSection={<IconPlus size="1rem" />}>Nodes</Tabs.Tab>
          <Tabs.Tab value="sources" leftSection={<IconDatabase size="1rem" />}>Sources</Tabs.Tab>
          <Tabs.Tab value="sinks" leftSection={<IconCloudUpload size="1rem" />}>Sinks</Tabs.Tab>
          <Tabs.Tab value="settings" leftSection={<IconSettingsAutomation size="1rem" />}>Settings</Tabs.Tab>
        </Tabs.List>

        <Tabs.Panel value="nodes">
          <ScrollArea h="calc(100vh - 120px)" offsetScrollbars type="always" px="xs">
            <Stack gap="lg" py="xs">
              {nodeCategories.map((cat) => (
                <Paper key={cat.title} withBorder p="xs" radius="md" bg={isDark ? 'dark.7' : 'gray.0'}>
                  <Text size="xs" fw={800} c="dimmed" mb="xs" px="xs" style={{ textTransform: 'uppercase', letterSpacing: '0.5px' }}>{cat.title}</Text>
                  <Stack gap={2}>
                    {cat.items.map(renderDraggableItem)}
                  </Stack>
                </Paper>
              ))}
            </Stack>
          </ScrollArea>
        </Tabs.Panel>

        <Tabs.Panel value="sources">
          <ScrollArea h="calc(100vh - 120px)" offsetScrollbars type="always" px="xs">
            <Stack gap="lg" py="xs">
              <Paper withBorder p="xs" radius="md" bg={isDark ? 'dark.7' : 'blue.0'}>
                <Group gap="xs" px="xs" mb="xs">
                  <IconDatabase size="1rem" color="var(--mantine-color-blue-6)" />
                  <Text size="xs" fw={800} c="blue.7" style={{ textTransform: 'uppercase', letterSpacing: '0.5px' }}>Databases</Text>
                </Group>
                <Stack gap={2}>
                  {[
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
                  ].map(renderDraggableItem)}
                </Stack>
              </Paper>

              <Paper withBorder p="xs" radius="md" bg={isDark ? 'dark.7' : 'indigo.0'}>
                <Group gap="xs" px="xs" mb="xs">
                  <IconCircles size="1rem" color="var(--mantine-color-indigo-6)" />
                  <Text size="xs" fw={800} c="indigo.7" style={{ textTransform: 'uppercase', letterSpacing: '0.5px' }}>Messaging & Streams</Text>
                </Group>
                <Stack gap={2}>
                  {[
                    { type: 'source', refId: 'new', label: 'Kafka', subType: 'kafka', icon: IconCircles, color: 'indigo', description: 'Consume from Kafka topics' },
                    { type: 'source', refId: 'new', label: 'NATS', subType: 'nats', icon: IconCircles, color: 'indigo', description: 'Consume from NATS JetStream' },
                    { type: 'source', refId: 'new', label: 'RabbitMQ Stream', subType: 'rabbitmq', icon: IconCircles, color: 'indigo', description: 'Consume from RMQ Stream' },
                    { type: 'source', refId: 'new', label: 'RabbitMQ Queue', subType: 'rabbitmq_queue', icon: IconCircles, color: 'indigo', description: 'Consume from AMQP queues' },
                    { type: 'source', refId: 'new', label: 'Redis Stream', subType: 'redis', icon: IconCircles, color: 'indigo', description: 'Consume from Redis Streams' },
                  ].map(renderDraggableItem)}
                </Stack>
              </Paper>

              <Paper withBorder p="xs" radius="md" bg={isDark ? 'dark.7' : 'cyan.0'}>
                <Group gap="xs" px="xs" mb="xs">
                  <IconWorld size="1rem" color="var(--mantine-color-cyan-6)" />
                  <Text size="xs" fw={800} c="cyan.7" style={{ textTransform: 'uppercase', letterSpacing: '0.5px' }}>Others</Text>
                </Group>
                <Stack gap={2}>
                  {[
                    { type: 'source', refId: 'new', label: 'Webhook', subType: 'webhook', icon: IconWorld, color: 'cyan', description: 'Receive HTTP POST events' },
                    { type: 'source', refId: 'new', label: 'Form Submission', subType: 'form', icon: IconWorld, color: 'cyan', description: 'Accept form submissions via HTTP' },
                    { type: 'source', refId: 'new', label: 'Cron / Schedule', subType: 'cron', icon: IconSettingsAutomation, color: 'cyan', description: 'Emit on a schedule' },
                    { type: 'source', refId: 'new', label: 'CSV / File', subType: 'csv', icon: IconFileSpreadsheet, color: 'cyan', description: 'Read rows from CSV/TSV' },
                    { type: 'source', refId: 'new', label: 'Google Sheets', subType: 'googlesheets', icon: IconFileSpreadsheet, color: 'cyan', description: 'Poll a Google Sheet' },
                    { type: 'source', refId: 'new', label: 'GraphQL', subType: 'graphql', icon: IconWorld, color: 'cyan', description: 'Receive GraphQL queries/mutations' },
                    { type: 'source', refId: 'new', label: 'gRPC', subType: 'grpc', icon: IconTerminal2, color: 'cyan', description: 'Receive gRPC Publish calls' },
                  ].map(renderDraggableItem)}
                </Stack>
              </Paper>
              
              <Box>
                <Text size="xs" fw={800} c="dimmed" mb="xs" px="xs" style={{ textTransform: 'uppercase', letterSpacing: '0.5px' }}>Existing Sources</Text>
                <Stack gap={2}>
                  {sources.map(s => renderDraggableItem({
                    type: 'source',
                    refId: s.id,
                    label: s.name,
                    subType: s.type,
                    icon: IconTable,
                    color: 'blue'
                  }))}
                </Stack>
              </Box>
            </Stack>
          </ScrollArea>
        </Tabs.Panel>

        <Tabs.Panel value="sinks">
          <ScrollArea h="calc(100vh - 120px)" offsetScrollbars type="always" px="xs">
            <Stack gap="lg" py="xs">
              <Paper withBorder p="xs" radius="md" bg={isDark ? 'dark.7' : 'green.0'}>
                <Group gap="xs" px="xs" mb="xs">
                  <IconDatabase size="1rem" color="var(--mantine-color-green-6)" />
                  <Text size="xs" fw={800} c="green.7" style={{ textTransform: 'uppercase', letterSpacing: '0.5px' }}>Databases</Text>
                </Group>
                <Stack gap={2}>
                  {[
                    { type: 'sink', refId: 'new', label: 'PostgreSQL', subType: 'postgres', icon: IconDatabase, color: 'green', description: 'Write rows to Postgres' },
                    { type: 'sink', refId: 'new', label: 'MySQL', subType: 'mysql', icon: IconDatabase, color: 'green', description: 'Write rows to MySQL' },
                    { type: 'sink', refId: 'new', label: 'MariaDB', subType: 'mariadb', icon: IconDatabase, color: 'green', description: 'Write rows to MariaDB' },
                    { type: 'sink', refId: 'new', label: 'SQL Server', subType: 'mssql', icon: IconDatabase, color: 'green', description: 'Write rows to SQL Server' },
                    { type: 'sink', refId: 'new', label: 'Oracle', subType: 'oracle', icon: IconDatabase, color: 'green', description: 'Write rows to Oracle' },
                    { type: 'sink', refId: 'new', label: 'MongoDB', subType: 'mongodb', icon: IconDatabase, color: 'green', description: 'Insert docs into MongoDB' },
                    { type: 'sink', refId: 'new', label: 'SQLite', subType: 'sqlite', icon: IconDatabase, color: 'green', description: 'Write rows to SQLite' },
                    { type: 'sink', refId: 'new', label: 'ClickHouse', subType: 'clickhouse', icon: IconDatabase, color: 'green', description: 'Insert into ClickHouse' },
                    { type: 'sink', refId: 'new', label: 'Elasticsearch', subType: 'elasticsearch', icon: IconSearch, color: 'green', description: 'Index documents into Elasticsearch' },
                    { type: 'sink', refId: 'new', label: 'YugabyteDB', subType: 'yugabyte', icon: IconDatabase, color: 'green', description: 'Write rows to Yugabyte' },
                    { type: 'sink', refId: 'new', label: 'Event Store', subType: 'eventstore', icon: IconDatabase, color: 'green', description: 'Unified Event Log (Event Sourcing)' },
                  ].map(renderDraggableItem)}
                </Stack>
              </Paper>

              <Paper withBorder p="xs" radius="md" bg={isDark ? 'dark.7' : 'teal.0'}>
                <Group gap="xs" px="xs" mb="xs">
                  <IconCircles size="1rem" color="var(--mantine-color-teal-6)" />
                  <Text size="xs" fw={800} c="teal.7" style={{ textTransform: 'uppercase', letterSpacing: '0.5px' }}>Messaging & Streams</Text>
                </Group>
                <Stack gap={2}>
                  {[
                    { type: 'sink', refId: 'new', label: 'Kafka', subType: 'kafka', icon: IconCircles, color: 'teal', description: 'Publish to Kafka topics' },
                    { type: 'sink', refId: 'new', label: 'NATS', subType: 'nats', icon: IconCircles, color: 'teal', description: 'Publish to NATS JetStream' },
                    { type: 'sink', refId: 'new', label: 'RabbitMQ Stream', subType: 'rabbitmq', icon: IconCircles, color: 'teal', description: 'Publish to RMQ Stream' },
                    { type: 'sink', refId: 'new', label: 'RabbitMQ Queue', subType: 'rabbitmq_queue', icon: IconCircles, color: 'teal', description: 'Publish to AMQP queues' },
                    { type: 'sink', refId: 'new', label: 'Redis Stream', subType: 'redis', icon: IconCircles, color: 'teal', description: 'Publish to Redis Streams' },
                    { type: 'sink', refId: 'new', label: 'Google Pub/Sub', subType: 'pubsub', icon: IconCircles, color: 'teal', description: 'Publish to GCP Pub/Sub' },
                    { type: 'sink', refId: 'new', label: 'AWS Kinesis', subType: 'kinesis', icon: IconCircles, color: 'teal', description: 'Publish to AWS Kinesis' },
                    { type: 'sink', refId: 'new', label: 'Apache Pulsar', subType: 'pulsar', icon: IconCircles, color: 'teal', description: 'Publish to Pulsar topics' },
                  ].map(renderDraggableItem)}
                </Stack>
              </Paper>

              <Paper withBorder p="xs" radius="md" bg={isDark ? 'dark.7' : 'lime.0'}>
                <Group gap="xs" px="xs" mb="xs">
                  <IconWorld size="1rem" color="var(--mantine-color-lime-6)" />
                  <Text size="xs" fw={800} c="lime.7" style={{ textTransform: 'uppercase', letterSpacing: '0.5px' }}>Notifications & Others</Text>
                </Group>
                <Stack gap={2}>
                  {[
                    { type: 'sink', refId: 'new', label: 'API / Webhook', subType: 'http', icon: IconCloudUpload, color: 'lime', description: 'POST events to HTTP endpoints' },
                    { type: 'sink', refId: 'new', label: 'SMTP (Email)', subType: 'smtp', icon: IconMail, color: 'lime', description: 'Send messages via email' },
                    { type: 'sink', refId: 'new', label: 'Telegram', subType: 'telegram', icon: IconMessage, color: 'lime', description: 'Send messages to Telegram' },
                    { type: 'sink', refId: 'new', label: 'Firebase (FCM)', subType: 'fcm', icon: IconMessage, color: 'lime', description: 'Push notifications via FCM' },
                    { type: 'sink', refId: 'new', label: 'File', subType: 'file', icon: IconDeviceFloppy, color: 'lime', description: 'Append to a local file' },
                    { type: 'sink', refId: 'new', label: 'Stdout', subType: 'stdout', icon: IconTerminal2, color: 'lime', description: 'Print to console' },
                    { type: 'sink', refId: 'new', label: 'Google Sheets', subType: 'googlesheets', icon: IconFileSpreadsheet, color: 'lime', description: 'Write to Google Sheets' },
                    { type: 'sink', refId: 'new', label: 'AWS S3', subType: 's3', icon: IconCloud, color: 'lime', description: 'Store objects in S3' },
                    { type: 'sink', refId: 'new', label: 'AWS S3 Parquet', subType: 's3-parquet', icon: IconCloud, color: 'lime', description: 'Store Parquet files in S3' },
                    { type: 'sink', refId: 'new', label: 'FTP / FTPS', subType: 'ftp', icon: IconCloud, color: 'lime', description: 'Upload files via FTP/FTPS' },
                  ].map(renderDraggableItem)}
                </Stack>
              </Paper>

              <Paper withBorder p="xs" radius="md" bg={isDark ? 'dark.7' : 'orange.0'}>
                <Group gap="xs" px="xs" mb="xs">
                  <IconArrowsSplit size="1rem" color="var(--mantine-color-orange-6)" />
                  <Text size="xs" fw={800} c="orange.7" style={{ textTransform: 'uppercase', letterSpacing: '0.5px' }}>Groups & Logic</Text>
                </Group>
                <Stack gap={2}>
                  {[
                    { type: 'sink', refId: 'new', label: 'Failover Group', subType: 'failover', icon: IconArrowsSplit, color: 'orange', description: 'Primary/Secondary failover logic' },
                  ].map(renderDraggableItem)}
                </Stack>
              </Paper>

              <Box>
                <Text size="xs" fw={800} c="dimmed" mb="xs" px="xs" style={{ textTransform: 'uppercase', letterSpacing: '0.5px' }}>Existing Sinks</Text>
                <Stack gap={2}>
                  {sinks.map(s => renderDraggableItem({
                    type: 'sink',
                    refId: s.id,
                    label: s.name,
                    subType: s.type,
                    icon: IconTable,
                    color: 'green'
                  }))}
                </Stack>
              </Box>
            </Stack>
          </ScrollArea>
        </Tabs.Panel>

        <Tabs.Panel value="settings">
          <ScrollArea h="calc(100vh - 120px)" offsetScrollbars type="always" px="xs">
            <Stack gap="lg" py="xs">
              <Paper withBorder p="md" radius="md" bg={isDark ? 'dark.7' : 'blue.0'}>
                <Group gap="xs" mb="md">
                  <ThemeIcon variant="light" color="blue" size="sm">
                    <IconShieldLock size="0.8rem" />
                  </ThemeIcon>
                  <Text fw={700} size="sm">Reliability Policy</Text>
                </Group>
                
                <Stack gap="md">
                  <Select
                    label="Dead Letter Sink"
                    placeholder="None"
                    data={(sinks || []).map((s: any) => ({ value: s.id, label: s.name }))}
                    value={deadLetterSinkID}
                    onChange={(val) => setDeadLetterSinkID(val || '')}
                    clearable
                    size="xs"
                    description="Sink for messages that exhaust retries"
                    error={deadLetterSinkID && !dlqSupportsRecovery ? "Sink type might not support recovery" : null}
                  />
                  <NumberInput
                    label="DLQ Alert Threshold"
                    placeholder="0 (Disabled)"
                    value={dlqThreshold}
                    onChange={(val) => setDlqThreshold(Number(val))}
                    min={0}
                    size="xs"
                    description="Trigger alert when DLQ reaches this count"
                  />
                  {deadLetterSinkID && !dlqSupportsRecovery && (
                    <Alert color="yellow" icon={<IconInfoCircle size="0.8rem" />} py="xs" styles={{ message: { fontSize: rem(10) } }}>
                      Requires a sink that can also act as a source for recovery.
                    </Alert>
                  )}
                  <Stack gap="xs">
                    <Checkbox 
                      label={<Text size="xs" fw={500}>Prioritize DLQ on startup</Text>}
                      checked={prioritizeDLQ}
                      onChange={(e) => setPrioritizeDLQ(e.currentTarget.checked)}
                      disabled={!!(deadLetterSinkID && !dlqSupportsRecovery)}
                    />
                    
                    <Checkbox 
                      label={<Text size="xs" fw={500}>Dry-Run Mode</Text>}
                      checked={dryRun}
                      onChange={(e) => setDryRun(e.currentTarget.checked)}
                    />
                  </Stack>
                </Stack>
              </Paper>
              
              <Paper withBorder p="md" radius="md" bg={isDark ? 'dark.7' : 'orange.0'}>
                <Group gap="xs" mb="md">
                  <ThemeIcon variant="light" color="orange" size="sm">
                    <IconRefresh size="0.8rem" />
                  </ThemeIcon>
                  <Text fw={700} size="sm">Retry & Reconnect</Text>
                </Group>

                <Stack gap="sm">
                  <NumberInput
                    label="Max Retries"
                    value={maxRetries}
                    onChange={(val) => setMaxRetries(Number(val))}
                    min={0}
                    max={100}
                    size="xs"
                  />
                  <Group grow gap="sm">
                    <TextInput
                      label="Retry Interval"
                      placeholder="100ms"
                      value={retryInterval}
                      onChange={(e) => setRetryInterval(e.currentTarget.value)}
                      size="xs"
                    />
                    <TextInput
                      label="Reconnect Interval"
                      placeholder="30s"
                      value={reconnectInterval}
                      onChange={(e) => setReconnectInterval(e.currentTarget.value)}
                      size="xs"
                    />
                  </Group>
                </Stack>
              </Paper>

              <Paper withBorder p="md" radius="md" bg={isDark ? 'dark.7' : 'teal.0'}>
                <Group gap="xs" mb="md">
                  <ThemeIcon variant="light" color="teal" size="sm">
                    <IconDatabase size="0.8rem" />
                  </ThemeIcon>
                  <Text fw={700} size="sm">Data Governance</Text>
                </Group>
                
                <Stack gap="sm">
                  <Select
                    label="Schema Validation"
                    placeholder="Disabled"
                    data={[
                      { value: '', label: 'Disabled' },
                      { value: 'json', label: 'JSON Schema' },
                      { value: 'avro', label: 'Avro' },
                      { value: 'protobuf', label: 'Protobuf' },
                    ]}
                    value={schemaType}
                    onChange={(val) => setSchemaType(val || '')}
                    size="xs"
                    clearable
                  />
                  
                  {schemaType && (
                    <Stack gap={4}>
                       <Text size="xs" fw={500}>Schema Definition</Text>
                       <Box style={{ border: '1px solid var(--mantine-color-gray-3)', borderRadius: '4px', overflow: 'hidden' }}>
                          <textarea
                            value={schema}
                            onChange={(e) => setSchema(e.currentTarget.value)}
                            placeholder={schemaType === 'json' ? '{ "type": "object", ... }' : 'Schema definition...'}
                            style={{
                              width: '100%',
                              height: '150px',
                              padding: '8px',
                              fontFamily: 'monospace',
                              fontSize: '11px',
                              border: 'none',
                              outline: 'none',
                              resize: 'vertical',
                              backgroundColor: isDark ? 'var(--mantine-color-dark-8)' : 'white',
                              color: isDark ? 'white' : 'black'
                            }}
                          />
                       </Box>
                    </Stack>
                  )}
                </Stack>
              </Paper>
            </Stack>
          </ScrollArea>
        </Tabs.Panel>
      </Tabs>
    </Drawer>
  );
}
