import { Stack, Title, Text, List, Code, TextInput, Select, Group } from '@mantine/core';
import { IconInfoCircle } from '@tabler/icons-react';
interface SourceSetupInstructionsProps {
  sourceType: string;
  useCDCChecked: boolean;
  config: Record<string, any>;
  updateConfig: (key: string, value: any) => void;
}

export function SourceSetupInstructions({ sourceType, useCDCChecked, config, updateConfig }: SourceSetupInstructionsProps) {
  if (!useCDCChecked && !['http', 'sap', 'dynamics365', 'mainframe', 'kafka', 'nats', 'redis', 'rabbitmq', 'rabbitmq_queue', 'csv', 'eventstore', 'discord', 'slack', 'twitter', 'facebook', 'instagram', 'linkedin', 'tiktok'].includes(sourceType)) {
     return (
      <Group gap="xs" c="dimmed">
        <IconInfoCircle size="1.2rem" />
        <Text size="sm">Select a source type or enable CDC to see setup instructions.</Text>
      </Group>
    );
  }

  switch (sourceType) {
    case 'postgres':
    case 'yugabyte':
      return (
        <Stack gap="xs">
          <Title order={5}>{sourceType === 'yugabyte' ? 'YugabyteDB' : 'PostgreSQL'} Setup</Title>
          <Text size="sm">To enable CDC, you need to:</Text>
          <List size="sm" withPadding>
            <List.Item>Set <Code>wal_level = logical</Code> in <Code>postgresql.conf</Code></List.Item>
            <List.Item>Restart the database</List.Item>
            <List.Item>Ensure the user has <Code>REPLICATION</Code> attributes or is a superuser</List.Item>
            <List.Item>Hermod will automatically create the publication and replication slot if they don't exist</List.Item>
          </List>
        </Stack>
      );
    case 'mysql':
    case 'mariadb':
      return (
        <Stack gap="xs">
          <Title order={5}>{sourceType === 'mariadb' ? 'MariaDB' : 'MySQL'} Setup</Title>
          <Text size="sm">To enable CDC, you need to:</Text>
          <List size="sm" withPadding>
            <List.Item>Enable binary logging: <Code>log-bin=mysql-bin</Code></List.Item>
            <List.Item>Set <Code>binlog_format=ROW</Code></List.Item>
            <List.Item>Set <Code>binlog_row_image=FULL</Code></List.Item>
            <List.Item>Grant <Code>REPLICATION SLAVE</Code>, <Code>REPLICATION CLIENT</Code>, and <Code>SELECT</Code> permissions to the user</List.Item>
          </List>
        </Stack>
      );
    case 'mongodb':
      return (
        <Stack gap="xs">
          <Title order={5}>MongoDB Setup</Title>
          <Text size="sm">Hermod uses Change Streams which require a Replica Set or Sharded Cluster.</Text>
          <List size="sm" withPadding>
            <List.Item>Ensure your MongoDB instance is running as a Replica Set</List.Item>
            <List.Item>The user must have <Code>read</Code> permissions on the databases/collections</List.Item>
          </List>
        </Stack>
      );
    case 'mssql':
      return (
        <Stack gap="xs">
          <Title order={5}>SQL Server Setup</Title>
          <Text size="sm">Enable CDC on the database and tables. You can use the "Auto Enable CDC" option or run these manually:</Text>
          <Code block>
            {`EXEC sys.sp_cdc_enable_db;
GO
EXEC sys.sp_cdc_enable_table
  @source_schema = N'dbo',
  @source_name   = N'MyTable',
  @role_name     = NULL;
GO`}
          </Code>
          <Text size="sm" c="dimmed">Note: SQL Server Agent must be running for CDC to work.</Text>
        </Stack>
      );
    case 'oracle':
      return (
        <Stack gap="xs">
          <Title order={5}>Oracle Setup</Title>
          <Text size="sm">Hermod supports Oracle CDC via LogMiner:</Text>
          <List size="sm" withPadding>
            <List.Item>Enable Archivelog mode</List.Item>
            <List.Item>Enable supplemental logging: <Code>ALTER DATABASE ADD SUPPLEMENTAL LOG DATA;</Code></List.Item>
            <List.Item>Grant <Code>SELECT ANY TRANSACTION</Code> and <Code>EXECUTE_CATALOG_ROLE</Code> to the user</List.Item>
          </List>
        </Stack>
      );
    case 'db2':
      return (
        <Stack gap="xs">
          <Title order={5}>IBM DB2 Setup</Title>
          <Text size="sm">CDC for DB2 is supported via the IBM CDC replication engine or polling.</Text>
          <List size="sm" withPadding>
            <List.Item>Ensure the user has <Code>DATAACCESS</Code> or similar permissions</List.Item>
            <List.Item>Tables must have <Code>DATA CAPTURE CHANGES</Code> enabled</List.Item>
          </List>
        </Stack>
      );
    case 'http':
      return (
        <Stack gap="xs">
          <Title order={5}>HTTP Polling Setup</Title>
          <Text size="sm">Poll data from REST or OData APIs at regular intervals.</Text>
          <TextInput
            label="API URL"
            placeholder="https://api.example.com/data"
            value={config.url || ''}
            onChange={(e) => updateConfig('url', e.target.value)}
            required
          />
          <Select
            label="Method"
            data={['GET', 'POST']}
            value={config.method || 'GET'}
            onChange={(val) => updateConfig('method', val || 'GET')}
          />
          <TextInput
            label="Polling Interval"
            placeholder="1m, 1h, 30s"
            value={config.poll_interval || '1m'}
            onChange={(e) => updateConfig('poll_interval', e.target.value)}
            description="How often to call the API"
          />
          <TextInput
            label="Headers (Comma separated)"
            placeholder="Authorization: Bearer ..., X-Custom: value"
            value={config.headers || ''}
            onChange={(e) => updateConfig('headers', e.target.value)}
          />
          <TextInput
            label="Data Path (GJSON)"
            placeholder="value (for OData) or result.items"
            value={config.data_path || ''}
            onChange={(e) => updateConfig('data_path', e.target.value)}
            description="Path to the array of records in the response"
          />
        </Stack>
      );

    case 'sap':
      return (
        <Stack gap="xs">
          <Title order={5}>SAP OData Setup</Title>
          <Text size="sm">Poll data from SAP via OData services.</Text>
          <List size="sm" withPadding>
            <List.Item>Ensure the OData service is activated in <Code>/IWFND/MAINT_SERVICE</Code>.</List.Item>
            <List.Item>The user needs authorizations to call the OData service and read the data.</List.Item>
            <List.Item>For delta polling, use the <Code>$filter</Code> parameter with a timestamp field.</List.Item>
          </List>
        </Stack>
      );
    case 'dynamics365':
      return (
        <Stack gap="xs">
          <Title order={5}>Dynamics 365 / Dataverse Setup</Title>
          <Text size="sm">Use OAuth 2.0 client-credentials to access the Dataverse Web API.</Text>
          <List size="sm" withPadding>
            <List.Item>Register an app in Microsoft Entra ID and grant Dataverse API permissions.</List.Item>
            <List.Item>Set Resource to your environment URL, e.g., <Code>https://org.crm.dynamics.com</Code>.</List.Item>
            <List.Item>Use an entity set name like <Code>accounts</Code> or <Code>contacts</Code>.</List.Item>
            <List.Item>For incremental reads, set <Code>ID Field</Code> to a sortable column like <Code>modifiedon</Code> and optionally a <Code>$filter</Code>.</List.Item>
          </List>
        </Stack>
      );
    case 'mainframe':
      return (
        <Stack gap="xs">
          <Title order={5}>Mainframe Integration</Title>
          <Text size="sm">Connect to Mainframe systems (Z/OS) via DB2 or VSAM wrappers.</Text>
          <List size="sm" withPadding>
            <List.Item>For DB2, ensure the IBM DB2 driver is accessible.</List.Item>
            <List.Item>For VSAM, a REST/OData wrapper is recommended for modern connectivity.</List.Item>
            <List.Item>Specify the schema and table for DB2 sources.</List.Item>
          </List>
        </Stack>
      );
    case 'cassandra':
    case 'scylladb':
      return (
        <Stack gap="xs">
          <Title order={5}>{sourceType === 'scylladb' ? 'ScyllaDB' : 'Cassandra'} Setup</Title>
          <Text size="sm">CDC must be enabled on the table:</Text>
          <Code block>
            {`ALTER TABLE my_table WITH cdc = true;`}
          </Code>
          <Text size="sm">Hermod will read from the CDC log tables.</Text>
        </Stack>
      );
    case 'clickhouse':
      return (
        <Stack gap="xs">
          <Title order={5}>ClickHouse Setup</Title>
          <Text size="sm">Hermod can read from ClickHouse tables.</Text>
          <List size="sm" withPadding>
            <List.Item>Ensure the user has <Code>SELECT</Code> permissions</List.Item>
            <List.Item>CDC is supported via polling or specialized engines if configured</List.Item>
          </List>
        </Stack>
      );
    case 'sqlite':
      return (
        <Stack gap="xs">
          <Title order={5}>SQLite Setup</Title>
          <Text size="sm">Provide the path to the SQLite database file.</Text>
          <List size="sm" withPadding>
            <List.Item>Hermod reads directly from the file</List.Item>
            <List.Item>Ensure the worker process has read permissions on the file and its directory</List.Item>
          </List>
        </Stack>
      );
    case 'kafka':
      return (
        <Stack gap="xs">
          <Title order={5}>Kafka Source</Title>
          <Text size="sm">Consumes messages from a Kafka topic.</Text>
          <List size="sm" withPadding>
            <List.Item>Provide the list of brokers and the topic name</List.Item>
            <List.Item>Group ID is used for offset management</List.Item>
            <List.Item>Supports SASL/Plain authentication</List.Item>
          </List>
        </Stack>
      );
    case 'nats':
      return (
        <Stack gap="xs">
          <Title order={5}>NATS Source</Title>
          <Text size="sm">Subscribes to a NATS subject.</Text>
          <List size="sm" withPadding>
            <List.Item>Specify the NATS server URL and Subject</List.Item>
            <List.Item>Optional Queue Group for load balancing across workers</List.Item>
          </List>
        </Stack>
      );
    case 'redis':
      return (
        <Stack gap="xs">
          <Title order={5}>Redis Source</Title>
          <Text size="sm">Reads from a Redis Stream.</Text>
          <List size="sm" withPadding>
            <List.Item>Provide the stream name and consumer group</List.Item>
            <List.Item>Hermod will manage offsets within the group</List.Item>
          </List>
        </Stack>
      );
    case 'rabbitmq':
      return (
        <Stack gap="xs">
          <Title order={5}>RabbitMQ Stream Source</Title>
          <Text size="sm">Reads from a RabbitMQ Stream.</Text>
          <List size="sm" withPadding>
            <List.Item>Requires RabbitMQ 3.9+ with the stream plugin enabled</List.Item>
            <List.Item>URL format: <Code>rabbitmq-stream://guest:guest@localhost:5552</Code></List.Item>
          </List>
        </Stack>
      );
    case 'rabbitmq_queue':
      return (
        <Stack gap="xs">
          <Title order={5}>RabbitMQ Queue Source</Title>
          <Text size="sm">Consumes from a standard RabbitMQ queue (AMQP).</Text>
          <List size="sm" withPadding>
            <List.Item>URL format: <Code>amqp://guest:guest@localhost:5672</Code></List.Item>
            <List.Item>Hermod will consume messages from the specified queue</List.Item>
          </List>
        </Stack>
      );
    case 'csv':
      return (
        <Stack gap="xs">
          <Title order={5}>CSV Source</Title>
          <Text size="sm">Reads data from a CSV file (Local, HTTP, or S3).</Text>
          <List size="sm" withPadding>
            <List.Item><b>Local:</b> Specify path or upload a file.</List.Item>
            <List.Item><b>HTTP:</b> Provide a URL to a CSV file. Optional custom headers for auth.</List.Item>
            <List.Item><b>S3:</b> Connect to AWS S3 or compatible storage (MinIO, etc).</List.Item>
            <List.Item>If "Has Header" is enabled, first row will be used as field names.</List.Item>
          </List>
        </Stack>
      );
    case 'eventstore':
      return (
        <Stack gap="xs">
          <Title order={5}>Event Store Source</Title>
          <Text size="sm">Replays events from the Hermod Event Store.</Text>
          <List size="sm" withPadding>
            <List.Item>Used for rebuilding projections (CQRS)</List.Item>
            <List.Item>Select the database driver and connection details</List.Item>
            <List.Item>Specify <Code>From Offset</Code> to start replaying from a specific point</List.Item>
          </List>
        </Stack>
      );
    case 'discord':
    case 'slack':
    case 'twitter':
    case 'facebook':
    case 'instagram':
    case 'linkedin':
    case 'tiktok':
      return (
        <Stack gap="xs">
          <Title order={5}>{sourceType.charAt(0).toUpperCase() + sourceType.slice(1)} Source</Title>
          <Text size="sm">Polls data from {sourceType}.</Text>
          <List size="sm" withPadding>
            <List.Item>Provide the required API credentials (tokens/IDs)</List.Item>
            <List.Item>Select the <Code>Mode</Code> to fetch posts, comments or statistics</List.Item>
            <List.Item>Adjust the <Code>Poll Interval</Code> to avoid rate limiting</List.Item>
            <List.Item>The engine will track the last processed message to avoid duplicates</List.Item>
          </List>
        </Stack>
      );
    default:
      return (
        <Group gap="xs" c="dimmed">
          <IconInfoCircle size="1.2rem" />
          <Text size="sm">Select a source type to see setup instructions.</Text>
        </Group>
      );
  }
}


