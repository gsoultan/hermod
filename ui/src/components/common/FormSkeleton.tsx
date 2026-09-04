import { Skeleton, Stack, Group } from '@mantine/core';

/**
 * Placeholder for a suspending form.
 *
 * Entity forms suspend on their reference lists (vhosts, workers, existing
 * entities). Without a boundary of their own the nearest one is the route's,
 * whose fallback is a full-viewport centred spinner — so the page title, the
 * breadcrumb and the whole nav sidebar vanished while three lists loaded, then
 * everything reappeared at once.
 *
 * This keeps the shell on screen and reserves roughly the form's height, so the
 * content that follows does not shove the page down when it arrives.
 */
export function FormSkeleton({ rows = 5 }: { rows?: number }) {
  return (
    <Stack gap="lg" aria-busy="true" aria-live="polite" aria-label="Loading form">
      <Group gap="md" wrap="nowrap">
        {Array.from({ length: 4 }, (_, i) => (
          <Skeleton key={i} height={28} radius="sm" style={{ flex: 1 }} />
        ))}
      </Group>
      <Skeleton height={1} />
      {Array.from({ length: rows }, (_, i) => (
        <Stack key={i} gap={6}>
          <Skeleton height={12} width="22%" radius="sm" />
          <Skeleton height={36} radius="md" />
        </Stack>
      ))}
      <Group justify="space-between" mt="md">
        <Skeleton height={36} width={96} radius="md" />
        <Skeleton height={36} width={128} radius="md" />
      </Group>
    </Stack>
  );
}
