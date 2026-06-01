import { TextInput, SimpleGrid } from '@mantine/core';

interface RetryPolicyFieldsProps {
  maxRetries: string | number | undefined;
  retryInterval: string | undefined;
  onChangeMaxRetries: (value: string) => void;
  onChangeRetryInterval: (value: string) => void;
}

/**
 * Renders basic retry policy fields used by many sinks.
 * Keep this component small and focused; it is purely presentational.
 * Balanced layout using SimpleGrid for consistent input alignment.
 */
export function RetryPolicyFields({
  maxRetries,
  retryInterval,
  onChangeMaxRetries,
  onChangeRetryInterval,
}: RetryPolicyFieldsProps) {
  return (
    <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
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
    </SimpleGrid>
  );
}
