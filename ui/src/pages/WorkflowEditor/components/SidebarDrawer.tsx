import { 
  Drawer, Tabs, Stack, Group, Paper, Text, ScrollArea, Box, ThemeIcon, UnstyledButton, rem, Title, useMantineColorScheme
} from '@mantine/core';
import { 
  IconDatabase, IconCloudUpload, IconWorld, IconSettingsAutomation, 
  IconFileSpreadsheet, IconCircles, IconMail, IconFilter, IconArrowsSplit, 
  IconGitBranch, IconGitMerge, IconNote, IconPlus, IconTable, IconTerminal2,
  IconMessage, IconVariable, IconEye, IconCode,
  IconShieldLock, IconSearch, IconCloud, IconPlaylist, IconDeviceFloppy, IconCheck
} from '@tabler/icons-react';
import { useWorkflowStore } from '../store/useWorkflowStore';

interface SidebarDrawerProps {
  onDragStart: (event: any, nodeType: string, refId: string, label: string, subType: string) => void;
  onAddItem: (type: string, refId: string, label: string, subType: string, Icon: any, color: string) => void;
  sources: any[];
  sinks: any[];
}

export function SidebarDrawer({ onDragStart, onAddItem, sources, sinks }: SidebarDrawerProps) {
  const store = useWorkflowStore();

  const nodeCategories = [
    {
      title: 'Common Transformations',
      items: [
        { type: 'transformation', refId: 'new', label: 'Mapping', subType: 'mapping', icon: IconFilter, color: 'violet' },
        { type: 'transformation', refId: 'new', label: 'Set Fields', subType: 'set', icon: IconVariable, color: 'violet' },
        { type: 'transformation', refId: 'new', label: 'Filter', subType: 'filter_data', icon: IconEye, color: 'violet' },
        { type: 'transformation', refId: 'new', label: 'Validate', subType: 'validate', icon: IconCheck, color: 'violet' },
        { type: 'transformation', refId: 'new', label: 'Mask Data', subType: 'mask', icon: IconShieldLock, color: 'violet' },
      ]
    },
    {
      title: 'Logic & Flow',
      items: [
        { type: 'condition', refId: 'new', label: 'Condition (If)', subType: 'condition', icon: IconArrowsSplit, color: 'indigo' },
        { type: 'switch', refId: 'new', label: 'Switch', subType: 'switch', icon: IconGitBranch, color: 'orange' },
        { type: 'merge', refId: 'new', label: 'Merge', subType: 'merge', icon: IconGitMerge, color: 'cyan' },
        { type: 'stateful', refId: 'new', label: 'Stateful', subType: 'stateful', icon: IconDatabase, color: 'pink' },
      ]
    },
    {
      title: 'Advanced Transformations',
      items: [
        { type: 'transformation', refId: 'new', label: 'DB Lookup', subType: 'db_lookup', icon: IconSearch, color: 'teal' },
        { type: 'transformation', refId: 'new', label: 'API Lookup', subType: 'api_lookup', icon: IconCloud, color: 'teal' },
        { type: 'transformation', refId: 'new', label: 'Pipeline', subType: 'pipeline', icon: IconPlaylist, color: 'teal' },
        { type: 'transformation', refId: 'new', label: 'Advanced', subType: 'advanced', icon: IconCode, color: 'teal' },
      ]
    },
    {
      title: 'Utilities',
      items: [
        { type: 'note', refId: 'new', label: 'Note', subType: 'note', icon: IconNote, color: 'yellow' },
      ]
    }
  ];

  const { colorScheme } = useMantineColorScheme();
  const isDark = colorScheme === 'dark';

  const renderDraggableItem = (item: any) => (
    <UnstyledButton
      key={item.label + item.refId}
      draggable
      onDragStart={(e) => onDragStart(e, item.type, item.refId, item.label, item.subType)}
      onClick={() => onAddItem(item.type, item.refId, item.label, item.subType, item.icon, item.color)}
      style={(theme) => ({
        display: 'block',
        width: '100%',
        padding: theme.spacing.xs,
        borderRadius: theme.radius.sm,
        color: isDark ? theme.colors.dark[0] : theme.black,
        '&:hover': {
          backgroundColor: isDark ? theme.colors.dark[6] : theme.colors.gray[0],
        },
      })}
    >
      <Group>
        <ThemeIcon variant="light" color={item.color} size="md">
          <item.icon style={{ width: rem(18), height: rem(18) }} />
        </ThemeIcon>
        <Box style={{ flex: 1 }}>
          <Text size="sm" fw={500}>{item.label}</Text>
          <Text size="xs" color="dimmed" style={{ textTransform: 'uppercase' }}>{item.type}</Text>
        </Box>
        <IconPlus size="1rem" color="var(--mantine-color-gray-4)" />
      </Group>
    </UnstyledButton>
  );

  return (
    <Drawer
      opened={store.drawerOpened}
      onClose={() => store.setDrawerOpened(false)}
      title={<Title order={4}>Add Nodes</Title>}
      position="right"
      size="md"
    >
      <Tabs defaultValue="nodes">
        <Tabs.List mb="md">
          <Tabs.Tab value="nodes">Toolbox</Tabs.Tab>
          <Tabs.Tab value="sources">Sources</Tabs.Tab>
          <Tabs.Tab value="sinks">Sinks</Tabs.Tab>
        </Tabs.List>

        <Tabs.Panel value="nodes">
          <ScrollArea h="calc(100vh - 180px)" offsetScrollbars>
            <Stack gap="xl">
              {nodeCategories.map((cat) => (
                <Box key={cat.title}>
                  <Text size="xs" fw={700} c="dimmed" mb="xs" style={{ textTransform: 'uppercase' }}>{cat.title}</Text>
                  <Stack gap={4}>
                    {cat.items.map(renderDraggableItem)}
                  </Stack>
                </Box>
              ))}
            </Stack>
          </ScrollArea>
        </Tabs.Panel>

        <Tabs.Panel value="sources">
          <ScrollArea h="calc(100vh - 180px)" offsetScrollbars>
            <Stack gap="md">
              <Paper withBorder p="xs" radius="md">
                <Text size="xs" fw={700} c="dimmed" mb="xs">DATABASES</Text>
                <Stack gap={4}>
                  {[
                    { type: 'source', refId: 'new', label: 'PostgreSQL', subType: 'postgres', icon: IconDatabase, color: 'blue' },
                    { type: 'source', refId: 'new', label: 'MySQL', subType: 'mysql', icon: IconDatabase, color: 'blue' },
                    { type: 'source', refId: 'new', label: 'MariaDB', subType: 'mariadb', icon: IconDatabase, color: 'blue' },
                    { type: 'source', refId: 'new', label: 'SQL Server', subType: 'mssql', icon: IconDatabase, color: 'blue' },
                    { type: 'source', refId: 'new', label: 'Oracle', subType: 'oracle', icon: IconDatabase, color: 'blue' },
                    { type: 'source', refId: 'new', label: 'MongoDB', subType: 'mongodb', icon: IconDatabase, color: 'blue' },
                    { type: 'source', refId: 'new', label: 'Cassandra', subType: 'cassandra', icon: IconDatabase, color: 'blue' },
                    { type: 'source', refId: 'new', label: 'SQLite', subType: 'sqlite', icon: IconDatabase, color: 'blue' },
                    { type: 'source', refId: 'new', label: 'ClickHouse', subType: 'clickhouse', icon: IconDatabase, color: 'blue' },
                    { type: 'source', refId: 'new', label: 'YugabyteDB', subType: 'yugabyte', icon: IconDatabase, color: 'blue' },
                    { type: 'source', refId: 'new', label: 'IBM DB2', subType: 'db2', icon: IconDatabase, color: 'blue' },
                    { type: 'source', refId: 'new', label: 'ScyllaDB', subType: 'scylladb', icon: IconDatabase, color: 'blue' },
                  ].map(renderDraggableItem)}
                </Stack>
              </Paper>

              <Paper withBorder p="xs" radius="md">
                <Text size="xs" fw={700} c="dimmed" mb="xs">MESSAGING & STREAMS</Text>
                <Stack gap={4}>
                  {[
                    { type: 'source', refId: 'new', label: 'Kafka', subType: 'kafka', icon: IconCircles, color: 'indigo' },
                    { type: 'source', refId: 'new', label: 'NATS', subType: 'nats', icon: IconCircles, color: 'indigo' },
                    { type: 'source', refId: 'new', label: 'RabbitMQ Stream', subType: 'rabbitmq', icon: IconCircles, color: 'indigo' },
                    { type: 'source', refId: 'new', label: 'RabbitMQ Queue', subType: 'rabbitmq_queue', icon: IconCircles, color: 'indigo' },
                    { type: 'source', refId: 'new', label: 'Redis Stream', subType: 'redis', icon: IconCircles, color: 'indigo' },
                  ].map(renderDraggableItem)}
                </Stack>
              </Paper>

              <Paper withBorder p="xs" radius="md">
                <Text size="xs" fw={700} c="dimmed" mb="xs">OTHERS</Text>
                <Stack gap={4}>
                  {[
                    { type: 'source', refId: 'new', label: 'Webhook', subType: 'webhook', icon: IconWorld, color: 'cyan' },
                    { type: 'source', refId: 'new', label: 'Cron / Schedule', subType: 'cron', icon: IconSettingsAutomation, color: 'cyan' },
                    { type: 'source', refId: 'new', label: 'CSV / File', subType: 'csv', icon: IconFileSpreadsheet, color: 'cyan' },
                  ].map(renderDraggableItem)}
                </Stack>
              </Paper>
              
              <Box>
                <Text size="xs" fw={700} c="dimmed" mb="xs">EXISTING SOURCES</Text>
                <Stack gap={4}>
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
          <ScrollArea h="calc(100vh - 180px)" offsetScrollbars>
            <Stack gap="md">
              <Paper withBorder p="xs" radius="md">
                <Text size="xs" fw={700} c="dimmed" mb="xs">DATABASES</Text>
                <Stack gap={4}>
                  {[
                    { type: 'sink', refId: 'new', label: 'PostgreSQL', subType: 'postgres', icon: IconDatabase, color: 'green' },
                    { type: 'sink', refId: 'new', label: 'MySQL', subType: 'mysql', icon: IconDatabase, color: 'green' },
                    { type: 'sink', refId: 'new', label: 'MariaDB', subType: 'mariadb', icon: IconDatabase, color: 'green' },
                    { type: 'sink', refId: 'new', label: 'SQL Server', subType: 'mssql', icon: IconDatabase, color: 'green' },
                    { type: 'sink', refId: 'new', label: 'Oracle', subType: 'oracle', icon: IconDatabase, color: 'green' },
                    { type: 'sink', refId: 'new', label: 'MongoDB', subType: 'mongodb', icon: IconDatabase, color: 'green' },
                    { type: 'sink', refId: 'new', label: 'Cassandra', subType: 'cassandra', icon: IconDatabase, color: 'green' },
                    { type: 'sink', refId: 'new', label: 'SQLite', subType: 'sqlite', icon: IconDatabase, color: 'green' },
                    { type: 'sink', refId: 'new', label: 'ClickHouse', subType: 'clickhouse', icon: IconDatabase, color: 'green' },
                    { type: 'sink', refId: 'new', label: 'YugabyteDB', subType: 'yugabyte', icon: IconDatabase, color: 'green' },
                  ].map(renderDraggableItem)}
                </Stack>
              </Paper>

              <Paper withBorder p="xs" radius="md">
                <Text size="xs" fw={700} c="dimmed" mb="xs">MESSAGING & STREAMS</Text>
                <Stack gap={4}>
                  {[
                    { type: 'sink', refId: 'new', label: 'Kafka', subType: 'kafka', icon: IconCircles, color: 'teal' },
                    { type: 'sink', refId: 'new', label: 'NATS', subType: 'nats', icon: IconCircles, color: 'teal' },
                    { type: 'sink', refId: 'new', label: 'RabbitMQ Stream', subType: 'rabbitmq', icon: IconCircles, color: 'teal' },
                    { type: 'sink', refId: 'new', label: 'RabbitMQ Queue', subType: 'rabbitmq_queue', icon: IconCircles, color: 'teal' },
                    { type: 'sink', refId: 'new', label: 'Redis Stream', subType: 'redis', icon: IconCircles, color: 'teal' },
                    { type: 'sink', refId: 'new', label: 'Google Pub/Sub', subType: 'pubsub', icon: IconCircles, color: 'teal' },
                    { type: 'sink', refId: 'new', label: 'AWS Kinesis', subType: 'kinesis', icon: IconCircles, color: 'teal' },
                    { type: 'sink', refId: 'new', label: 'Apache Pulsar', subType: 'pulsar', icon: IconCircles, color: 'teal' },
                  ].map(renderDraggableItem)}
                </Stack>
              </Paper>

              <Paper withBorder p="xs" radius="md">
                <Text size="xs" fw={700} c="dimmed" mb="xs">NOTIFICATIONS & OTHERS</Text>
                <Stack gap={4}>
                  {[
                    { type: 'sink', refId: 'new', label: 'API / Webhook', subType: 'http', icon: IconCloudUpload, color: 'lime' },
                    { type: 'sink', refId: 'new', label: 'SMTP (Email)', subType: 'smtp', icon: IconMail, color: 'lime' },
                    { type: 'sink', refId: 'new', label: 'Telegram', subType: 'telegram', icon: IconMessage, color: 'lime' },
                    { type: 'sink', refId: 'new', label: 'Firebase (FCM)', subType: 'fcm', icon: IconMessage, color: 'lime' },
                    { type: 'sink', refId: 'new', label: 'File', subType: 'file', icon: IconDeviceFloppy, color: 'lime' },
                    { type: 'sink', refId: 'new', label: 'Stdout', subType: 'stdout', icon: IconTerminal2, color: 'lime' },
                  ].map(renderDraggableItem)}
                </Stack>
              </Paper>

              <Box>
                <Text size="xs" fw={700} c="dimmed" mb="xs">EXISTING SINKS</Text>
                <Stack gap={4}>
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
      </Tabs>
    </Drawer>
  );
}
