import { Box, Group, Text, Title } from '@mantine/core';
import type { ReactNode } from 'react';

interface SectionHeaderProps {
  /** The same words as the tab that opened it. */
  title: string;
  lede?: ReactNode;
  /** A badge beside the title, such as the virtual host shown. */
  meta?: ReactNode;
  /** Buttons on the right; keep it to one primary and at most one other. */
  actions?: ReactNode;
}

/**
 * The heading of a tab inside a page. The page already has its title, so a tab
 * gets a plain second-level heading: no icon tile, no coloured panel.
 */
export function SectionHeader({ title, lede, meta, actions }: SectionHeaderProps) {
  return (
    <Group justify="space-between" align="flex-start" wrap="wrap" gap="sm" mb="md">
      <Box style={{ minWidth: 0, flex: '1 1 320px' }}>
        <Group gap="sm" align="center" wrap="wrap">
          <Title order={2} size="h3">{title}</Title>
          {meta}
        </Group>
        {lede && <Text c="dimmed" size="sm" mt={4} maw={760}>{lede}</Text>}
      </Box>
      {actions && <Group gap="xs" wrap="wrap">{actions}</Group>}
    </Group>
  );
}
