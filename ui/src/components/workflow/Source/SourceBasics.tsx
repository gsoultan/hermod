import { toGroupedSelectData } from '@/utils/selectData';
import { useState } from 'react';
import { validateName, validateType, validateVHost } from '@/hooks/useEntityBasicsForm';
import { TextInput, Group, Select, Button, Stack, Fieldset, SimpleGrid } from '@mantine/core';
import type { Worker, Source } from '@/types';
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
  // Validation runs as the user types rather than surfacing server-side after
  // submit. Errors only appear once a field has been touched, so a fresh form
  // is not covered in red before anything has been entered.
  const [touched, setTouched] = useState<Record<string, boolean>>({});
  const nameError = touched.name ? validateName(source.name) : undefined;
  const typeError = touched.type ? validateType(source.type) : undefined;
  const vhostError = touched.vhost ? validateVHost(source.vhost, !embedded) : undefined;

  return (
    <Fieldset legend="General Settings" radius="md">
      <Stack gap="sm">
        <TextInput
          label="Name"
          description="How this connection appears in workflows"
          placeholder="Production DB"
          value={source.name}
          onChange={(e) => handleSourceChange({ name: e.target.value })}
          onBlur={() => setTouched((t) => ({ ...t, name: true }))}
          error={nameError}
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
              onBlur={() => setTouched((t) => ({ ...t, vhost: true }))}
              error={vhostError}
              required
              description="Project or environment namespace"
              mih={80}
            />
            <Select 
              label="Worker (Optional)" 
              placeholder="Assign to a specific worker" 
              data={Array.isArray(workers) ? workers.map((w: Worker) => ({ value: w.id, label: w.name || w.id })) : []}
              value={source.worker_id}
              onChange={(val) => handleSourceChange({ worker_id: val || '' })}
              clearable
              description="Dedicated processing instance"
              mih={80}
            />
          </SimpleGrid>
        )}
        <Group align="flex-end" grow>
          {!embedded ? (
            <Select
              label="Type"
              placeholder="Select source type"
              description="System this source reads from"
              data={toGroupedSelectData(sourceTypes)}
              value={source.type}
              onChange={(val) => handleSourceChange({ type: val || '' })}
              onBlur={() => setTouched((t) => ({ ...t, type: true }))}
              error={typeError}
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


