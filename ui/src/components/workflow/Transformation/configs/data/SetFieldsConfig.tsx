import { Stack, Alert, Text, Divider, rem } from '@mantine/core';
import { IconInfoCircle } from '@tabler/icons-react';
import { Suspense, lazy } from 'react';
import { JsonObjectInput } from '@/components/common/JsonObjectInput';
import { useColumnFields } from '@/components/common/useColumnFields';

const SetFieldEditor = lazy(() =>
  import('../../SetFieldEditor').then((m) => ({ default: m.SetFieldEditor }))
);

interface SetFieldsConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any, replace?: boolean) => void;
  nodeId: string;
  availableFields: any[];
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
  const { columnFields, replaceColumnFields } = useColumnFields(config, nodeId, updateNodeConfig);
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
      {/* Moved here from TransformationForm's inline block when the duplicate
          config panes were removed; this is the only raw-JSON editor now. */}
      <Divider label="Raw JSON" labelPosition="center" />
      <JsonObjectInput
        label="Fields (JSON)"
        placeholder='{"column.user.role": "admin", "column.status": 1}'
        value={columnFields}
        onChange={replaceColumnFields}
        minRows={8}
        styles={{ input: { fontFamily: 'monospace', fontSize: 'var(--mantine-font-size-xs)' } }}
        description="Specify fields to set using 'column.path' format."
      />
    </Stack>
  );
}
