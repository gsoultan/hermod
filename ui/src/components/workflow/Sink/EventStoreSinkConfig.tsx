import { TextInput, Select, Stack, Code, Text } from '@mantine/core';
import { IconDatabase } from '@tabler/icons-react';
import { FormRow } from '@/components/common/FormRow';

interface EventStoreSinkConfigProps {
  config: any;
  updateConfig: (key: string, value: any) => void;
}

/**
 * The event store sink appends every message to an append-only SQL table.
 *
 * The driver names offered are the ones registered in the binary, because
 * `sql.Open(driver, dsn)` is what the factory calls. They are not always the
 * same spelling `BuildConnectionString` switches on — PostgreSQL is `pgx` to
 * database/sql and `postgres` to that helper — which is why the DSN is
 * required here rather than assembled from host/port fields.
 */
export function EventStoreSinkConfig({ config, updateConfig }: EventStoreSinkConfigProps) {
  return (
    <Stack gap="md">
      <FormRow cols={2}>
        <Select
          label="Driver"
          value={config.driver || ''}
          onChange={(value) => updateConfig('driver', value || '')}
          data={[
            { value: 'pgx', label: 'PostgreSQL' },
            { value: 'mysql', label: 'MySQL / MariaDB' },
            { value: 'sqlite', label: 'SQLite' },
            { value: 'sqlserver', label: 'SQL Server' },
            { value: 'clickhouse', label: 'ClickHouse' },
            { value: 'oracle', label: 'Oracle' },
          ]}
          description="The schema is created on first connect."
          required
        />
        <TextInput
          label="DSN"
          placeholder="postgres://user:pass@host:5432/events?sslmode=require"
          value={config.dsn || ''}
          onChange={(e) => updateConfig('dsn', e.currentTarget.value)}
          description="The full connection string for the driver above."
          leftSection={<IconDatabase size="1rem" />}
          required
        />
      </FormRow>

      <Text size="xs" c="dimmed">
        Templates below are Go templates over the message — <Code>{'{{.table}}'}</Code>,{' '}
        <Code>{'{{.id}}'}</Code>, <Code>{'{{.operation}}'}</Code> and any field of the payload. A
        message carrying its own stream id in metadata overrides the template.
      </Text>

      <FormRow cols={2}>
        <TextInput
          label="Stream ID template"
          placeholder="{{.table}}:{{.id}}"
          value={config.stream_id_tpl || ''}
          onChange={(e) => updateConfig('stream_id_tpl', e.currentTarget.value)}
          description="Blank uses table:id, falling back to the table alone when there is no id."
        />
        <TextInput
          label="Event type template"
          placeholder="{{.table}}.{{.operation}}"
          value={config.event_type_tpl || ''}
          onChange={(e) => updateConfig('event_type_tpl', e.currentTarget.value)}
          description="Blank uses the message's operation."
        />
      </FormRow>
    </Stack>
  );
}
