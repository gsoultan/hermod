import { Stack, Alert, Text, rem } from '@mantine/core';
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

export function ConditionConfig({
  config,
  updateNodeConfig,
  nodeId,
  availableFields,
}: ConditionConfigProps) {
  return (
    <Stack gap="md">
      <Alert
        icon={<IconInfoCircle size={rem(18)} />}
        color="indigo"
        variant="light"
        radius="md"
        title="Conditional Branching"
      >
        <Text size="sm">
          Branch the workflow based on conditions. If conditions match, the message follows the
          'true' branch; otherwise, it follows the 'false' branch.
        </Text>
      </Alert>
      <Suspense fallback={<Text size="xs" p="md">Loading conditions editor...</Text>}>
        <FilterEditor
          conditions={config.conditions || []}
          availableFields={availableFields}
          onChange={(val: any) => updateNodeConfig(nodeId, { conditions: val })}
        />
      </Suspense>
    </Stack>
  );
}
