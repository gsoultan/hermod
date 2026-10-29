import { Anchor, Box, Group, Stack, Text, Title } from '@mantine/core';
import { IconChevronLeft } from '@tabler/icons-react';
import { Link } from '@tanstack/react-router';
import type { ReactNode } from 'react';

interface PageHeaderProps {
  /** The same words as the sidebar link or row that led here. */
  title: string;
  lede?: string;
  /** Buttons on the right; keep it to one primary and at most one other. */
  actions?: ReactNode;
  back?: { to: string; label: string; search?: Record<string, string> };
  /** A line under the title, such as a state pill or a count. */
  meta?: ReactNode;
}

/** The one page header: title left, actions right, nothing above it. */
export function PageHeader({ title, lede, actions, back, meta }: PageHeaderProps) {
  return (
    <Stack gap={4} mb="lg">
      {back && (
        <Anchor component={Link} to={back.to} search={back.search as any} size="sm" c="dimmed" style={{ display: 'inline-flex', alignItems: 'center', gap: 4, width: 'fit-content' }}>
          <IconChevronLeft size="0.9rem" aria-hidden /> {back.label}
        </Anchor>
      )}
      <Group justify="space-between" align="flex-start" wrap="wrap" gap="sm">
        <Box style={{ minWidth: 0 }}>
          <Title order={1} size="h2" lh={1.2}>{title}</Title>
          {lede && <Text c="dimmed" size="sm" mt={4}>{lede}</Text>}
          {meta && <Box mt={6}>{meta}</Box>}
        </Box>
        {actions && <Group gap="xs" wrap="wrap">{actions}</Group>}
      </Group>
    </Stack>
  );
}
