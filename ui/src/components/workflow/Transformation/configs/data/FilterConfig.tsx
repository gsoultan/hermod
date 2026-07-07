import { Stack, Alert, Text } from '@mantine/core';
import { IconInfoCircle } from '@tabler/icons-react';
import { Suspense, lazy } from 'react';

const FilterEditor = lazy(() =>
  import('../../FilterEditor').then((m) => ({ default: m.FilterEditor }))
);

interface FilterConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
  availableFields: any[];
}

export function FilterConfig({ config, updateNodeConfig, nodeId, availableFields }: FilterConfigProps) {
  return (
    <Stack gap="md">
      <Alert icon={<IconInfoCircle size="1rem" />} color="violet">
        <Text size="sm">Define conditions to filter out messages. Only matching messages will continue.</Text>
      </Alert>
      <Suspense fallback={<Text size="xs">Loading editor...</Text>}>
        <FilterEditor 
          conditions={config.conditions || []} 
          availableFields={availableFields}
          onChange={(val: any) => updateNodeConfig(nodeId, { conditions: val })}
        />
      </Suspense>
    </Stack>
  );
}
