import { Stack, Alert, Text, Divider } from '@mantine/core';
import { IconInfoCircle } from '@tabler/icons-react';
import { Suspense, lazy } from 'react';
import { JsonObjectInput } from '@/components/common/JsonObjectInput';
import { useColumnFields } from '@/components/common/useColumnFields';

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
  const { columnFields, replaceColumnFields } = useColumnFields(config, nodeId, updateNodeConfig);
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
      {/* Moved here from TransformationForm's inline block when the duplicate
          config panes were removed; this is the only raw-JSON editor now. */}
      <Divider label="Raw JSON" labelPosition="center" />
      <JsonObjectInput
        label="Config (JSON)"
        placeholder='{"column.user.name": "lower(source.user.name)"}'
        value={columnFields}
        onChange={replaceColumnFields}
        minRows={8}
        styles={{ input: { fontFamily: 'monospace', fontSize: 'var(--mantine-font-size-xs)' } }}
      />
    </Stack>
  );
}
