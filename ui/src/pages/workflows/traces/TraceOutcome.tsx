import { Alert, Badge, Text } from '@mantine/core';
import { IconCircleCheck, IconCircleDashed, IconCircleX } from '@tabler/icons-react';
import type { TraceOutcome } from './traceOutcome';

/** The journey's verdict, above its steps. */
export function TraceOutcomeBanner({ outcome, nodeName }: { outcome: TraceOutcome; nodeName?: (id: string) => string | undefined }) {
  if (outcome.kind === 'failed') {
    const where = nodeName?.(outcome.failedAt.node_id) || outcome.failedAt.node_id;
    return (
      <Alert color="red" variant="light" icon={<IconCircleX size="1.1rem" />} title="Failed">
        <Text size="sm" style={{ overflowWrap: 'anywhere' }}>
          Stopped at {where}: {outcome.failedAt.error}
        </Text>
        {outcome.deliveredTo.length > 0 && (
          <Text size="xs" c="dimmed" mt={4}>Still delivered to {outcome.deliveredTo.join(', ')}.</Text>
        )}
      </Alert>
    );
  }
  if (outcome.kind === 'delivered') {
    return (
      <Alert color="green" variant="light" icon={<IconCircleCheck size="1.1rem" />} title="Delivered">
        <Text size="sm" style={{ overflowWrap: 'anywhere' }}>
          Every step succeeded and the message was written to {outcome.deliveredTo.join(', ')}.
        </Text>
      </Alert>
    );
  }
  return (
    <Alert color="gray" variant="light" icon={<IconCircleDashed size="1.1rem" />} title="Did not reach a sink">
      <Text size="sm">
        No step failed, but nothing was written: a filter or a branch ended the journey before any sink.
      </Text>
    </Alert>
  );
}

/**
 * A trace's status in the list, from the error count the list already returns.
 * A trace recorded before the count existed carries none, and shows nothing
 * rather than a guess.
 */
export function TraceListStatus({ errorCount }: { errorCount: number | undefined }) {
  if (typeof errorCount !== 'number') return null;
  if (errorCount > 0) {
    return (
      <Badge color="red" variant="light" size="sm" leftSection={<IconCircleX size="0.8rem" />} style={{ flex: 'none' }}>
        {errorCount === 1 ? '1 error' : `${errorCount} errors`}
      </Badge>
    );
  }
  return (
    <Badge color="green" variant="light" size="sm" leftSection={<IconCircleCheck size="0.8rem" />} style={{ flex: 'none' }}>
      No errors
    </Badge>
  );
}
