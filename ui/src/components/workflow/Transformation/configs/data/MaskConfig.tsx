import { Stack, Autocomplete, Select, Alert, Text } from '@mantine/core';
import { IconInfoCircle } from '@tabler/icons-react';

interface MaskConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
  availableFields: string[];
}

export function MaskConfig({ config, updateNodeConfig, nodeId, availableFields }: MaskConfigProps) {
  return (
    <Stack gap="md">
      <Alert icon={<IconInfoCircle size="1rem" />} color="violet">
        <Text size="sm">Obfuscate sensitive data like emails, names, or credit card numbers.</Text>
      </Alert>
      <Autocomplete 
        label="Field" 
        placeholder="e.g. email (use * for all)" 
        data={availableFields || []}
        value={config.field || ''} 
        onChange={(val) => updateNodeConfig(nodeId, { field: val })} 
        description="Field to mask. Use * to scan all fields."
      />
      <Select 
        label="Mask Type" 
        data={[
          { label: 'All (****)', value: 'all' },
          { label: 'Partial (ab****yz)', value: 'partial' },
          { label: 'Email (a****@b.com)', value: 'email' },
          { label: 'Auto PII Detection', value: 'pii' },
        ]} 
        value={config.maskType || 'all'} 
        onChange={(val) => updateNodeConfig(nodeId, { maskType: val || 'all' })} 
      />
    </Stack>
  );
}
