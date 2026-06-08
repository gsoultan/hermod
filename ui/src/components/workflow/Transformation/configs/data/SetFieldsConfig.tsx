import { Stack, Alert, Text, rem } from '@mantine/core';
import { IconInfoCircle } from '@tabler/icons-react';
import { Suspense, lazy } from 'react';

const SetFieldEditor = lazy(() =>
  import('../../SetFieldEditor').then((m) => ({ default: m.SetFieldEditor }))
);

interface SetFieldsConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any, replace?: boolean) => void;
  nodeId: string;
  availableFields: string[];
  incomingPayload?: any;
  onAddFromSource: (path: string) => void;
  addField: (path?: string, value?: string) => void;
}

export function SetFieldsConfig({
  config,
  updateNodeConfig,
  nodeId,
  availableFields,
  incomingPayload,
  onAddFromSource,
  addField,
}: SetFieldsConfigProps) {
  return (
    <Stack gap="md">
      <Alert
        icon={<IconInfoCircle size={rem(18)} />}
        color="violet"
        variant="light"
        radius="md"
        title="Field Transformation"
      >
        <Text size="sm">
          Add new fields or override existing ones. Use <code>{'{{.field}}'}</code> to reference
          incoming data.
        </Text>
      </Alert>
      <Suspense fallback={<Text size="xs" p="md">Loading editor...</Text>}>
        <SetFieldEditor
          selectedNode={{ id: nodeId, data: config }}
          updateNodeConfig={updateNodeConfig}
          availableFields={availableFields}
          incomingPayload={incomingPayload}
          transType="set"
          onAddFromSource={onAddFromSource}
          addField={addField}
        />
      </Suspense>
    </Stack>
  );
}
