import { Stack, Alert, Text } from '@mantine/core';
import { IconInfoCircle } from '@tabler/icons-react';
import { Suspense, lazy } from 'react';

const FilterEditor = lazy(() =>
  import('../../FilterEditor').then((m) => ({ default: m.FilterEditor }))
);

interface ConditionConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
  availableFields: string[];
}

export function ConditionConfig({ config, updateNodeConfig, nodeId, availableFields }: ConditionConfigProps) {
  return (
    <Stack gap="md">
      <Alert icon={<IconInfoCircle size="1rem" />} color="indigo">
        <Text size="sm">Branch the workflow based on conditions. If matched, the 'true' branch is followed; otherwise 'false'.</Text>
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
