import { Select, Stack, Text, MultiSelect, Alert } from '@mantine/core';
import type { FC } from 'react';
import { IconInfoCircle } from '@tabler/icons-react';
import { useSinkOptions } from '@/hooks/useSinkOptions';
export type FailoverSinkConfigProps = {
  config: any;
  /** Sinks the caller already has. Without them the form asks for the list. */
  sinks?: any[];
  currentSinkId?: string;
  updateConfig: (key: string, value: any) => void;
}

export const FailoverSinkConfig: FC<FailoverSinkConfigProps> = ({
  config,
  sinks,
  currentSinkId,
  updateConfig,
}) => {
  const known = useSinkOptions(sinks);

  // Filter out the current sink to avoid self-reference and recursive failovers for simplicity
  const availableSinks = (known || [])
    .filter(s => s.id !== currentSinkId && s.type !== 'failover')
    .map(s => ({ value: s.id, label: `${s.name} (${s.type})` }));

  const fallbackIds = (config.fallback_ids || '').split(',').filter(Boolean);

  return (
    <Stack gap="sm">
      <Alert icon={<IconInfoCircle size="1rem" />} color="blue">
        Failover Group allows you to define a primary sink and multiple fallback sinks.
        If the primary fails, the engine will automatically try the fallbacks in order.
      </Alert>
      
      <Select
        label="Primary Sink"
        placeholder={known === null ? 'Loading…' : 'Select primary sink'}
        data={availableSinks}
        value={config.primary_id || ''}
        onChange={(val) => updateConfig('primary_id', val || '')}
        required
        searchable
      />

      <Select
        label="Strategy"
        data={[
          { label: 'Sequential Failover', value: 'failover' },
          { label: 'Round Robin', value: 'round-robin' },
        ]}
        value={config.strategy || 'failover'}
        onChange={(val) => updateConfig('strategy', val || 'failover')}
        description="Sequential: Try primary, then fallbacks. Round-Robin: Distribute load across all."
      />

      <MultiSelect
        label="Fallback Sinks"
        placeholder="Select fallback sinks (ordered)"
        data={availableSinks}
        value={fallbackIds}
        onChange={(val) => updateConfig('fallback_ids', val.join(','))}
        searchable
        description="These sinks will be tried in the order selected if the primary fails."
      />

      {config.primary_id && fallbackIds.includes(config.primary_id) && (
        <Text size="xs" color="red">Primary sink cannot be a fallback sink.</Text>
      )}
    </Stack>
  );
}


