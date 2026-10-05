import { Code, Input, SegmentedControl, Stack, Text, TextInput } from '@mantine/core';

interface ResponseModeFieldsProps {
  config: Record<string, any>;
  updateConfig: (key: string, value: any) => void;
}

/** The value the transports read as "hold the caller" (pkg/comm/reply). */
export const RESPONSE_MODE_SYNC = 'sync';

/**
 * ResponseModeFields is the choice a webhook or gRPC source makes about its
 * caller: answer as soon as the record is queued, or hold the caller until the
 * workflow has finished and answer with what happened.
 *
 * It writes `response_mode` and `response_timeout`, the keys the webhook
 * endpoint and the gRPC service read.
 */
export function ResponseModeFields({ config, updateConfig }: ResponseModeFieldsProps) {
  const waits = config.response_mode === RESPONSE_MODE_SYNC;

  return (
    <Stack gap="xs">
      <Input.Wrapper
        label="Response"
        description="Asynchronous answers as soon as the record is queued. Synchronous holds the caller until the workflow has finished with it."
      >
        <SegmentedControl
          mt={6}
          aria-label="Response"
          value={waits ? RESPONSE_MODE_SYNC : 'async'}
          onChange={(value) => updateConfig('response_mode', value)}
          data={[
            { label: 'Asynchronous', value: 'async' },
            { label: 'Synchronous', value: RESPONSE_MODE_SYNC },
          ]}
        />
      </Input.Wrapper>

      {waits && (
        <>
          <TextInput
            label="Response timeout"
            placeholder="30s"
            value={config.response_timeout || ''}
            onChange={(e) => updateConfig('response_timeout', e.target.value)}
            description="How long a caller is held, for example 10s or 2m. 30s when empty, 5m at most."
          />
          <Text size="xs" c="dimmed" data-testid="sync-response-explanation">
            The answer carries a <Code>status</Code>, an <Code>error</Code> when the record failed, and the{' '}
            <Code>record</Code> as the workflow left it. The status is <Code>delivered</Code>, <Code>completed</Code>{' '}
            (the workflow ran and had nothing to write), <Code>dead_lettered</Code>, <Code>failed</Code>, or{' '}
            <Code>pending</Code> when the timeout ran out first. A pending record is still being processed, so do not
            send it again.
          </Text>
        </>
      )}
    </Stack>
  );
}
