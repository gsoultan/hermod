import { TextInput, Stack, Group, Text, Code, Alert } from '@mantine/core';
import { IconInfoCircle } from '@tabler/icons-react';

interface SnowflakeSinkConfigProps {
  form: any;
}

export function SnowflakeSinkConfig({ form }: SnowflakeSinkConfigProps) {
  return (
    <Stack gap="md">
      <Alert icon={<IconInfoCircle size="1rem" />} color="blue" title="Snowflake Connection">
        <Text size="xs">
          Use the Snowflake connection string format: <Code>user:password@account/database/schema?warehouse=wh</Code>
        </Text>
      </Alert>

      <form.Field name="config.connection_string">
        {(field: any) => (
          <TextInput
            label="Connection String"
            placeholder="user:pass@account/db/schema?warehouse=wh"
            required
            value={field.state.value || ''}
            onChange={(e) => field.handleChange(e.target.value)}
            error={field.state.meta.errors?.[0]}
          />
        )}
      </form.Field>

      <Group grow>
        <form.Field name="config.database">
          {(field: any) => (
            <TextInput
              label="Database (Override)"
              placeholder="MY_DB"
              value={field.state.value || ''}
              onChange={(e) => field.handleChange(e.target.value)}
            />
          )}
        </form.Field>
        <form.Field name="config.schema">
          {(field: any) => (
            <TextInput
              label="Schema (Override)"
              placeholder="PUBLIC"
              value={field.state.value || ''}
              onChange={(e) => field.handleChange(e.target.value)}
            />
          )}
        </form.Field>
      </Group>

      <form.Field name="config.warehouse">
        {(field: any) => (
          <TextInput
            label="Warehouse"
            placeholder="COMPUTE_WH"
            value={field.state.value || ''}
            onChange={(e) => field.handleChange(e.target.value)}
          />
        )}
      </form.Field>

      <form.Field name="config.role">
        {(field: any) => (
          <TextInput
            label="Role"
            placeholder="ACCOUNTADMIN"
            value={field.state.value || ''}
            onChange={(e) => field.handleChange(e.target.value)}
          />
        )}
      </form.Field>
    </Stack>
  );
}
