import { Alert, Select, Stack, TagsInput, Text, TextInput } from '@mantine/core';
import { IconInfoCircle } from '@tabler/icons-react';

interface JoinFieldsConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
}

export function JoinFieldsConfig({ config, updateNodeConfig, nodeId }: JoinFieldsConfigProps) {
  return (

    <Stack gap="xs">
      <Select
        label="Join Mode"
        data={[
          { label: 'Store (Save current record to state)', value: 'store' },
          { label: 'Lookup (Enrich from state)', value: 'lookup' },
        ]}
        value={config.mode || 'lookup'}
        onChange={(val: string | null) => updateNodeConfig(nodeId, { mode: val || 'lookup' })}
      />
      <TextInput
        label="Join Key (Message Path)"
        placeholder="e.g. order_id"
        value={config.key || ''}
        onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(nodeId, { key: e.target.value })}
        description="Field in the current message used to match records."
      />
      <TextInput
        label="Storage Namespace"
        placeholder="default"
        value={config.namespace || ''}
        onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(nodeId, { namespace: e.target.value })}
        description="Use namespaces to separate different join datasets."
      />
      {config.mode === 'lookup' && (
        <>
          <TextInput
            label="Joined Field Prefix"
            placeholder="joined_"
            value={config.prefix || ''}
            onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(nodeId, { prefix: e.target.value })}
          />
          <TagsInput
            label="Specific Fields to Extract"
            placeholder="Leave empty for all fields"
            value={config.fields || []}
            onChange={(val: string[]) => updateNodeConfig(nodeId, { fields: val })}
          />
        </>
      )}
      <Alert icon={<IconInfoCircle size="1rem" />} color="indigo" py="xs" mt="md">
        <Text size="xs">Enrich messages by joining them with data previously 'Stored' by other messages sharing the same key.</Text>
      </Alert>
    </Stack>
  
  );
}
