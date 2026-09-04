import { Badge, Tooltip } from '@mantine/core';
import { IconPlugConnected, IconPlugConnectedX } from '@tabler/icons-react';

/**
 * Says out loud whether the live status badges on this page can be trusted.
 *
 * The status socket previously had no reconnect and no visible state, so a
 * dropped connection left every badge frozen on its last value — a stopped
 * pipeline still reading "running" with nothing to indicate the page had gone
 * deaf. For an operations tool that is worse than showing nothing.
 */
export function LiveStatusIndicator({ connected }: { connected: boolean }) {
  return (
    <Tooltip
      label={
        connected
          ? 'Live status is streaming from the server.'
          : 'Live status is disconnected — the statuses below may be out of date. Reconnecting…'
      }
      position="left"
      withArrow
    >
      <Badge
        size="sm"
        variant="light"
        color={connected ? 'teal' : 'orange'}
        leftSection={
          connected ? <IconPlugConnected size="0.75rem" /> : <IconPlugConnectedX size="0.75rem" />
        }
        role="status"
        aria-live="polite"
      >
        {connected ? 'Live' : 'Reconnecting'}
      </Badge>
    </Tooltip>
  );
}
