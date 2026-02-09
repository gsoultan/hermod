import { TextInput, Group, Select, Button, Stack, Fieldset, SimpleGrid } from '@mantine/core';
import { useEffect } from 'react';
import type { Worker, Source } from '../../types';
import type { FC } from 'react';
import { IconInfoCircle } from '@tabler/icons-react';
interface SourceBasicsProps {
  source: Source;
  handleSourceChange: (updates: Partial<Source>) => void;
  embedded?: boolean;
  availableVHostsList: string[];
  workers: Worker[];
  sourceTypes: { value: string; label: string; group?: string }[];
  setShowSetup: (show: boolean) => void;
}

export const SourceBasics: FC<SourceBasicsProps> = ({ 
  source, 
  handleSourceChange, 
  embedded, 
  availableVHostsList, 
  workers, 
  sourceTypes,
  setShowSetup
}) => {
  useEffect(() => {
    // Debug log to catch undefined data passed to Selects on first mount
    try {
      // eslint-disable-next-line no-console
      console.log('SourceBasics mount:', {
        vhostsType: typeof availableVHostsList,
        vhostsIsArray: Array.isArray(availableVHostsList),
        vhostsLen: Array.isArray(availableVHostsList) ? availableVHostsList.length : 'n/a',
        workersIsArray: Array.isArray(workers),
        workersLen: Array.isArray(workers) ? workers.length : 'n/a',
        sourceTypesIsArray: Array.isArray(sourceTypes),
        sourceTypesLen: Array.isArray(sourceTypes) ? sourceTypes.length : 'n/a',
      });
    } catch {}
  }, []);
  return (
    <Fieldset legend="General Settings" radius="md">
      <Stack gap="sm">
        <TextInput 
          label="Name" 
          placeholder="Production DB" 
          value={source.name}
          onChange={(e) => handleSourceChange({ name: e.target.value })}
          required
        />
        {!embedded && (
          <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
            <Select 
              label="VHost" 
              placeholder="Select a virtual host" 
              data={Array.isArray(availableVHostsList) ? availableVHostsList : []}
              value={source.vhost}
              onChange={(val) => handleSourceChange({ vhost: val || '' })}
              required
            />
            <Select 
              label="Worker (Optional)" 
              placeholder="Assign to a specific worker" 
              data={Array.isArray(workers) ? workers.map((w: Worker) => ({ value: w.id, label: w.name || w.id })) : []}
              value={source.worker_id}
              onChange={(val) => handleSourceChange({ worker_id: val || '' })}
              clearable
            />
          </SimpleGrid>
        )}
        <Group align="flex-end" grow>
          {!embedded ? (
            <Select
              label="Type"
              placeholder="Select source type"
              data={Array.isArray(sourceTypes) ? sourceTypes : []}
              value={source.type}
              onChange={(val) => handleSourceChange({ type: val || '' })}
              required
              searchable
            />
          ) : (
            <TextInput 
              label="Type" 
              value={source.type}
              readOnly
              variant="filled"
              styles={{ input: { textTransform: 'uppercase', fontWeight: 600 } }}
            />
          )}
          <Button 
            variant="light" 
            color="blue" 
            leftSection={<IconInfoCircle size="1rem" />}
            onClick={() => setShowSetup(true)}
            style={{ flex: 'none' }}
          >
            Setup Guide
          </Button>
        </Group>
      </Stack>
    </Fieldset>
  );
};


