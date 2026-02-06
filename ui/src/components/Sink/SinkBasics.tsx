import { Select, Stack, TextInput } from '@mantine/core';

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
  vhostOptions,
  workerOptions,
  sinkTypes,
}: SinkBasicsProps) {
  return (
    <Stack gap="sm">
      <TextInput
        label="Name"
        placeholder="NATS Sink"
        value={name}
        onChange={(e) => onChangeName(e.currentTarget.value)}
        required
      />

      {!embedded && (
        <Select
          label="VHost"
          placeholder="Select a virtual host"
          data={Array.isArray(vhostOptions) ? vhostOptions : []}
          value={vhost}
          onChange={(val) => onChangeVHost(val || '')}
          required
        />
      )}

      {!embedded && (
        <Select
          label="Worker (Optional)"
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
          placeholder="Select sink type"
          data={Array.isArray(sinkTypes) ? sinkTypes : []}
          value={type}
          onChange={(val) => onChangeType(val || '')}
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
