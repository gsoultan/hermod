import { Stack, Alert, Text } from '@mantine/core';
import { IconInfoCircle } from '@tabler/icons-react';
import { Suspense, lazy } from 'react';

const MappingEditor = lazy(() =>
  import('../../MappingEditor').then((m) => ({ default: m.MappingEditor }))
);

interface MappingConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
  availableFields: any[];
  incomingPayload?: any;
}

export function MappingConfig({ config, updateNodeConfig, nodeId, availableFields, incomingPayload }: MappingConfigProps) {
  return (
    <Stack gap="md">
      <Alert icon={<IconInfoCircle size="1rem" />} color="violet">
        <Text size="sm">Reshape your data by mapping incoming fields to a new structure.</Text>
      </Alert>
      <Suspense fallback={<Text size="xs">Loading editor...</Text>}>
        <MappingEditor 
          selectedNode={{ id: nodeId, data: config }}
          updateNodeConfig={updateNodeConfig}
          availableFields={availableFields}
          incomingPayload={incomingPayload}
        />
      </Suspense>
    </Stack>
  );
}
