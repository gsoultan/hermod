import { Stack, Alert, Text } from '@mantine/core';
import { IconInfoCircle } from '@tabler/icons-react';
import { Suspense, lazy } from 'react';

const SetFieldEditor = lazy(() =>
  import('../../SetFieldEditor').then((m) => ({ default: m.SetFieldEditor }))
);

interface AdvancedConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any, replace?: boolean) => void;
  nodeId: string;
  availableFields: any[];
  incomingPayload?: any;
  transType: string;
  onAddFromSource: (path: string) => void;
  addField: () => void;
}

export function AdvancedConfig({ config, updateNodeConfig, nodeId, availableFields, incomingPayload, transType, onAddFromSource, addField }: AdvancedConfigProps) {
  return (
    <Stack gap="md">
      <Alert icon={<IconInfoCircle size="1rem" />} color="blue">
        <Text size="xs" fw={700}>Advanced expressions: operation(source.field)</Text>
      </Alert>
      <Suspense fallback={<Text size="xs">Loading editor...</Text>}>
        <SetFieldEditor
          selectedNode={{ id: nodeId, data: config }}
          updateNodeConfig={updateNodeConfig}
          availableFields={availableFields}
          incomingPayload={incomingPayload}
          transType={transType}
          onAddFromSource={onAddFromSource}
          addField={addField}
        />
      </Suspense>
    </Stack>
  );
}
