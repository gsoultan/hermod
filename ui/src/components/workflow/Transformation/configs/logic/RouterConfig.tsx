import { Stack, Alert, Text } from '@mantine/core';
import { IconInfoCircle } from '@tabler/icons-react';
import { Suspense, lazy } from 'react';

const RouterEditor = lazy(() =>
  import('../../RouterEditor').then((m) => ({ default: m.RouterEditor }))
);

interface RouterConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
  availableFields: any[];
}

export function RouterConfig({ config, updateNodeConfig, nodeId, availableFields }: RouterConfigProps) {
  return (
    <Stack gap="md">
      <Alert icon={<IconInfoCircle size="1rem" />} color="indigo">
        <Text size="sm">Route messages to different branches based on multiple rules. The first matching rule wins.</Text>
      </Alert>
      <Suspense fallback={<Text size="xs">Loading editor...</Text>}>
        <RouterEditor 
          selectedNode={{ id: nodeId, data: config }}
          updateNodeConfig={updateNodeConfig}
          availableFields={availableFields}
        />
      </Suspense>
    </Stack>
  );
}
