import { Box, Group, Text, Title } from '@mantine/core';
import type { ReactNode } from 'react';
import { PageHeader } from '@/components/rows';

interface AdminListHeaderProps {
  title: string;
  lede: string;
  actions?: ReactNode;
  /** Shown as a tab of Settings › Workspace, which already says where you are. */
  embedded?: boolean;
  back: { to: string; label: string; search?: Record<string, string> };
}

/**
 * People, virtual hosts and workers open on their own and as tabs of Settings ›
 * Workspace. On their own they get the page header and the way back; inside
 * the tab a section heading, so the page does not carry two titles.
 */
export function AdminListHeader({ title, lede, actions, embedded, back }: AdminListHeaderProps) {
  if (!embedded) return <PageHeader title={title} lede={lede} actions={actions} back={back} />;
  return (
    <Group justify="space-between" align="flex-start" wrap="wrap" gap="sm" mb="md">
      <Box style={{ minWidth: 0 }}>
        <Title order={3}>{title}</Title>
        <Text c="dimmed" size="sm" mt={4}>{lede}</Text>
      </Box>
      {actions && <Group gap="xs">{actions}</Group>}
    </Group>
  );
}
