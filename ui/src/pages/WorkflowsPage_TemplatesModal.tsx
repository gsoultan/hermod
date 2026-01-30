import { Button, Card, SimpleGrid, Stack, Text, ThemeIcon, Group } from '@mantine/core'
import { IconGitBranch, IconSend, IconHistory } from '@tabler/icons-react'

type TemplateDef = {
  name: string
  description: string
  icon: any
  color: string
  data: any
}

const TEMPLATES: TemplateDef[] = [
  {
    name: 'World Case: GDPR Masking & High-Value Routing',
    description:
      'Enterprise-grade pipeline with PII protection, intelligent routing, and zero-loss reliability via an integrated Dead Letter Sink.',
    icon: IconGitBranch,
    color: 'blue',
    data: {
      name: 'World Case: GDPR Masking & High-Value Routing',
      dead_letter_sink_id: 'snk-global-dlq',
      prioritize_dlq: false,
      max_retries: 3,
      nodes: [
        {
          id: 'node_postgres',
          type: 'source',
          config: { label: 'PostgreSQL CDC (Orders)', type: 'postgres', ref_id: 'src-orders-db' },
          x: 100,
          y: 250,
        },
        {
          id: 'node_transform',
          type: 'transformation',
          config: {
            label: 'GDPR & Format',
            transType: 'pipeline',
            steps:
              '[{"transType": "mask", "field": "customer_email", "maskType": "email"}, {"transType": "mapping", "field": "status_id", "mapping": "{\"1\": \"PENDING\", \"2\": \"PAID\", \"3\": \"CANCELLED\"}"}, {"transType": "set", "column.processed_by": "Hermod-Worker-01"}]',
          },
          x: 400,
          y: 250,
        },
        {
          id: 'node_condition',
          type: 'condition',
          config: { label: 'High Value? (> 1000)', field: 'total_amount', operator: '>', value: '1000' },
          x: 700,
          y: 250,
        },
        {
          id: 'node_set_priority',
          type: 'transformation',
          config: { label: 'Mark Priority', transType: 'set', 'column.priority': 'true' },
          x: 950,
          y: 150,
        },
        { id: 'node_telegram', type: 'sink', config: { label: 'Telegram Alert', type: 'telegram', ref_id: 'snk-alerts' }, x: 1200, y: 100 },
        { id: 'node_kafka', type: 'sink', config: { label: 'Kafka High Priority', type: 'kafka', ref_id: 'snk-kafka-prod' }, x: 1200, y: 200 },
        { id: 'node_mongodb', type: 'sink', config: { label: 'MongoDB Archive', type: 'mongodb', ref_id: 'snk-mongo-archive' }, x: 1200, y: 400 },
        { id: 'node_dlq_sink', type: 'sink', config: { label: 'Global DLQ (Postgres)', type: 'postgres', ref_id: 'snk-global-dlq' }, x: 1200, y: 550 },
      ],
      edges: [
        { id: 'edge_1', source_id: 'node_postgres', target_id: 'node_transform' },
        { id: 'edge_2', source_id: 'node_transform', target_id: 'node_condition' },
        { id: 'edge_3', source_id: 'node_condition', target_id: 'node_set_priority', config: { label: 'true' } },
        { id: 'edge_4', source_id: 'node_set_priority', target_id: 'node_telegram' },
        { id: 'edge_5', source_id: 'node_set_priority', target_id: 'node_kafka' },
        { id: 'edge_6', source_id: 'node_condition', target_id: 'node_mongodb', config: { label: 'false' } },
      ],
    },
  },
  {
    name: 'HTTP Webhook to Slack',
    description: 'Receive data via HTTP Webhook and forward it to Slack/Discord.',
    icon: IconSend,
    color: 'green',
    data: {
      name: 'Webhook to Slack',
      nodes: [
        { id: 'n1', type: 'source', config: { label: 'Incoming Webhook', type: 'webhook', ref_id: 'new' }, x: 100, y: 100 },
        { id: 'n2', type: 'sink', config: { label: 'Slack Webhook', type: 'http', ref_id: 'new' }, x: 400, y: 100 },
      ],
      edges: [{ id: 'e1', source_id: 'n1', target_id: 'n2' }],
    },
  },
  {
    name: 'Reliability: Advanced Recovery & DLQ Managed Re-drive',
    description:
      'Zero data loss strategy with managed recovery. Uses a Switch node to detect recovered messages and route them through a "Fix-up" transformation before reaching the primary sink.',
    icon: IconHistory,
    color: 'orange',
    data: {
      name: 'Reliability: Advanced Recovery & DLQ Managed Re-drive',
      dead_letter_sink_id: 'snk-postgres-dlq',
      prioritize_dlq: true,
      max_retries: 5,
      retry_interval: '10s',
      nodes: [
        {
          id: 'node_pg_source',
          type: 'source',
          config: {
            label: 'Primary Postgres CDC',
            type: 'postgres',
            ref_id: 'src-primary-db',
          },
          x: 50,
          y: 250,
        },
        {
          id: 'node_recovery_switch',
          type: 'switch',
          config: {
            label: 'Is Recovery?',
            branches: [
              { label: 'recovery', condition: '{{eq .metadata._hermod_source "recovery"}}' },
              { label: 'primary', condition: 'default' },
            ],
          },
          x: 350,
          y: 250,
        },
        {
          id: 'node_fixup_transform',
          type: 'transformation',
          config: {
            label: 'Recovery Fix-up',
            transType: 'set',
            'column._recovered_at': '{{now}}',
            'column._recovery_status': 'processed',
          },
          x: 600,
          y: 150,
        },
        {
          id: 'node_clickhouse_sink',
          type: 'sink',
          config: {
            label: 'ClickHouse Analytics (Primary Sink)',
            type: 'clickhouse',
            ref_id: 'snk-analytics-db',
          },
          x: 900,
          y: 250,
        },
        {
          id: 'node_dlq_sink',
          type: 'sink',
          config: {
            label: 'Postgres DLQ (Recovery Storage)',
            type: 'postgres',
            ref_id: 'snk-postgres-dlq',
          },
          x: 900,
          y: 450,
        },
      ],
      edges: [
        {
          id: 'edge_1',
          source_id: 'node_pg_source',
          target_id: 'node_recovery_switch',
        },
        {
          id: 'edge_2',
          source_id: 'node_recovery_switch',
          sourceHandle: 'recovery',
          target_id: 'node_fixup_transform',
        },
        {
          id: 'edge_3',
          source_id: 'node_fixup_transform',
          target_id: 'node_clickhouse_sink',
        },
        {
          id: 'edge_4',
          source_id: 'node_recovery_switch',
          sourceHandle: 'primary',
          target_id: 'node_clickhouse_sink',
        },
      ],
    },
  },
]

export default function TemplatesModal({ onUseTemplate }: { onUseTemplate: (data: any) => void }) {
  return (
    <Stack>
      <Text size="sm" c="dimmed">
        Choose a pre-built template to jumpstart your workflow development.
      </Text>
      <SimpleGrid cols={{ base: 1, md: 2 }} spacing="md">
        {TEMPLATES.map((template, index) => (
          <Card key={index} withBorder shadow="sm" radius="md" padding="lg">
            <Stack gap="sm">
              <Group justify="space-between">
                <ThemeIcon size="lg" radius="md" variant="light" color={template.color}>
                  <template.icon size="1.2rem" />
                </ThemeIcon>
                <Button
                  size="xs"
                  variant="light"
                  color={template.color}
                  onClick={() => onUseTemplate(template.data)}
                >
                  Use Template
                </Button>
              </Group>
              <Text fw={700}>{template.name}</Text>
              <Text size="xs" c="dimmed" style={{ minHeight: 40 }}>
                {template.description}
              </Text>
            </Stack>
          </Card>
        ))}
      </SimpleGrid>
    </Stack>
  )
}
