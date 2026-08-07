import { toGroupedSelectData } from '@/utils/selectData';
import { useState } from 'react';
import { validateName, validateType, validateVHost } from '@/hooks/useEntityBasicsForm';
import { Select, Stack, TextInput, Switch } from '@mantine/core';

interface SinkBasicsProps {
  embedded?: boolean;
  name: string;
  onChangeName: (value: string) => void;
  vhost: string;
  onChangeVHost: (value: string) => void;
  workerId: string;
  onChangeWorkerId: (value: string) => void;
  type: string;
  onChangeType: (value: string) => void;
  sequential?: boolean;
  onChangeSequential?: (value: boolean) => void;
  vhostOptions: any[];
  workerOptions: any[];
  sinkTypes: any[];
}

export function SinkBasics({
  embedded,
  name,
  onChangeName,
  vhost,
  onChangeVHost,
  workerId,
  onChangeWorkerId,
  type,
  onChangeType,
  sequential,
  onChangeSequential,
  vhostOptions,
  workerOptions,
  sinkTypes,
}: SinkBasicsProps) {
  // See SourceBasics: validate while typing, reveal only after blur.
  const [touched, setTouched] = useState<Record<string, boolean>>({});
  const nameError = touched.name ? validateName(name) : undefined;
  const typeError = touched.type ? validateType(type) : undefined;
  const vhostError = touched.vhost ? validateVHost(vhost, !embedded) : undefined;

  return (
    <Stack gap="sm">
      <TextInput
        label="Name"
        description="How this destination appears in workflows"
        placeholder="NATS Sink"
        value={name}
        onChange={(e) => onChangeName(e.currentTarget.value)}
        onBlur={() => setTouched((t) => ({ ...t, name: true }))}
        error={nameError}
        required
      />

      {onChangeSequential && (
        <Switch
          label="Sequential Execution"
          description="If enabled, this sink executes in-line with the workflow and can block downstream nodes if it fails."
          checked={sequential}
          onChange={(e) => onChangeSequential(e.currentTarget.checked)}
        />
      )}

      {!embedded && (
        <Select
          label="VHost"
          description="Project or environment namespace"
          placeholder="Select a virtual host"
          data={Array.isArray(vhostOptions) ? vhostOptions : []}
          value={vhost}
          onChange={(val) => onChangeVHost(val || '')}
          onBlur={() => setTouched((t) => ({ ...t, vhost: true }))}
          error={vhostError}
          required
        />
      )}

      {!embedded && (
        <Select
          label="Worker (Optional)"
          description="Pin to a specific processing instance"
          placeholder="Assign to a specific worker"
          data={Array.isArray(workerOptions) ? workerOptions : []}
          value={workerId}
          onChange={(val) => onChangeWorkerId(val || '')}
          clearable
        />
      )}

      {!embedded ? (
        <Select
          label="Type"
          description="Destination system this sink writes to"
          placeholder="Select sink type"
          data={toGroupedSelectData(sinkTypes)}
          value={type}
          onChange={(val) => onChangeType(val || '')}
          onBlur={() => setTouched((t) => ({ ...t, type: true }))}
          error={typeError}
          required
          searchable
        />
      ) : (
        <TextInput
          label="Type"
          value={type}
          readOnly
          variant="filled"
          styles={{ input: { textTransform: 'uppercase', fontWeight: 600 } }}
        />
      )}
    </Stack>
  );
}
