import { lazy, Suspense } from 'react';
import type { Condition } from '../../FilterEditor';

const FilterEditor = lazy(() =>
  import('../../FilterEditor').then((m) => ({ default: m.FilterEditor })),
);

import { Alert, Box, Stack, Switch, Text, TextInput } from '@mantine/core';
import { IconInfoCircle } from '@tabler/icons-react';

interface FilterDataConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
  availableFields?: any[];
  /** Distinguishes the branching `condition` node from a filtering one. */
  transType?: string;
}

export function FilterDataConfig({ config, updateNodeConfig, nodeId, availableFields, transType }: FilterDataConfigProps) {
    let conditions: Condition[] = []
    try {
      conditions = typeof config.conditions === 'string'
        ? JSON.parse(config.conditions || '[]')
        : (config.conditions || [])
    } catch {
      conditions = []
    }
    if (conditions.length === 0 && config.field) {
      conditions.push({
        field: config.field,
        operator: config.operator || '=',
        value: config.value || '',
      })
    }
    return (
      <Stack gap="xs">
        <Box mb="md" p="xs" style={{ border: '1px dashed var(--mantine-color-gray-3)', borderRadius: 'var(--mantine-radius-sm)' }}>
          <Switch 
            label="Set result as boolean field instead of filtering" 
            checked={!!config.asField || transType === 'validate'}
            disabled={transType === 'validate'}
            onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(nodeId, { asField: e.currentTarget.checked })}
            mb={config.asField || transType === 'validate' ? 'xs' : 0}
          />
          {(config.asField || transType === 'validate') && (
            <TextInput 
              label="Target Field Name"
              placeholder="e.g. is_valid"
              value={config.targetField || ''}
              onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(nodeId, { targetField: e.target.value })}
              size="xs"
            />
          )}
        </Box>
        <Suspense fallback={null}>
          <FilterEditor
            conditions={conditions}
            availableFields={availableFields || []}
            onChange={(next: Condition[]) =>
              updateNodeConfig(nodeId, { conditions: JSON.stringify(next) })
            }
          />
        </Suspense>
        <Alert icon={<IconInfoCircle size="1rem" />} color={transType === 'condition' ? 'yellow' : 'violet'} py="xs" mt="md">
          <Stack gap={4}>
            <Text size="xs">
              {transType === 'condition' 
                ? 'Conditions branch the flow. Use "true" and "false" labels on outgoing edges.' 
                : 'Filters will stop the message if the condition is not met.'}
            </Text>
          </Stack>
        </Alert>
      </Stack>
    );
  }
