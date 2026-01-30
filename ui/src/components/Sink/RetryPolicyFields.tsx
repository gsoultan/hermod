import { Group, TextInput } from '@mantine/core';

interface RetryPolicyFieldsProps {
  maxRetries: string | number | undefined;
  retryInterval: string | undefined;
  onChangeMaxRetries: (value: string) => void;
  onChangeRetryInterval: (value: string) => void;
}

/**
 * Renders basic retry policy fields used by many sinks.
 * Keep this component small and focused; it is purely presentational.
 */
export function RetryPolicyFields({
  maxRetries,
  retryInterval,
  onChangeMaxRetries,
  onChangeRetryInterval,
}: RetryPolicyFieldsProps) {
  return (
    <Group grow>
      <TextInput
        label="Max Retries"
        placeholder="3"
        size="xs"
        value={maxRetries ?? ''}
        onChange={(e) => onChangeMaxRetries(e.currentTarget.value)}
      />
      <TextInput
        label="Retry Interval"
        placeholder="1s"
        size="xs"
        value={retryInterval ?? ''}
        onChange={(e) => onChangeRetryInterval(e.currentTarget.value)}
      />
    </Group>
  );
}
