import { TextInput, Text, Code } from '@mantine/core';

export function SSESinkConfig({ config, updateConfig }: { config: any; updateConfig: (k: string, v: any) => void }) {
  const stream = config.stream || 'default';
  return (
    <>
      <TextInput
        label="Stream Name"
        placeholder="default"
        value={config.stream || ''}
        onChange={(e) => updateConfig('stream', e.currentTarget.value)}
        description="Logical stream name clients will subscribe to"
        required
      />
      <Text size="sm" c="dimmed" mt="xs">
        Clients can subscribe via Server-Sent Events at <Code>/api/sse/stream?stream={stream}</Code>
      </Text>
    </>
  );
}
