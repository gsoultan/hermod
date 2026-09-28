import { Alert, Button, Stack, Text } from '@mantine/core';
import { IconAlertTriangle } from '@tabler/icons-react';

/**
 * Says that a node's input skipped the nodes before it.
 *
 * With no run to read from, the editor hands a node the nearest payload up the
 * graph -- usually the source's sample -- so Test and the Live Preview work on
 * data none of the nodes in between have touched. A running workflow does apply
 * them. That is how an api_lookup's Test API Call passed while the refresh and
 * Run Simulation, which run every node, were refused: the node before it had
 * emptied the fields its body read, and nothing on screen said the Test had
 * never seen that node.
 */
export function UpstreamNotRunNotice({
  skipped,
  onRun,
  running,
}: {
  skipped: number;
  /** Runs the workflow on the sample, so this node reads what the node before it emits. */
  onRun?: () => void;
  running?: boolean;
}) {
  if (skipped <= 0) return null;
  const one = skipped === 1;
  return (
    <Alert
      data-testid="upstream-not-run-notice"
      color="yellow"
      variant="light"
      icon={<IconAlertTriangle size="1rem" />}
      py="xs"
    >
      <Stack gap={6} align="flex-start">
        {/* "No output", not "not run": a node that ran and failed or dropped the
            sample leaves the same gap. */}
        <Text size="xs">
          {one ? 'The node before this one has' : `The ${skipped} nodes before this one have`} no output for this
          sample yet, so Test and the Live Preview use the data from further up, without{' '}
          {one ? 'its' : 'their'} changes. A running workflow applies them.
        </Text>
        {onRun && (
          <Button size="compact-xs" variant="light" color="yellow" onClick={onRun} loading={running}>
            {one ? 'Run it on the sample' : 'Run them on the sample'}
          </Button>
        )}
      </Stack>
    </Alert>
  );
}
