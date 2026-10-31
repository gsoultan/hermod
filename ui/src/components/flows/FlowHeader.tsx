import { ActionIcon, Box, Group, Stack, Tooltip } from '@mantine/core';
import { useMediaQuery } from '@mantine/hooks';
import { IconChevronLeft } from '@tabler/icons-react';
import { Link } from '@tanstack/react-router';
import type { ReactNode } from 'react';

interface FlowHeaderProps {
  back: { to: string; params?: Record<string, string>; label: string };
  /** The flow's name: a heading in the view, a field in the editor. */
  title: ReactNode;
  /** One line under the name: its state and where it runs. */
  meta?: ReactNode;
  /** The page's buttons. On a phone they move to a bar along the bottom. */
  actions?: ReactNode;
  /** ⋯, which stays by the name at every width. */
  menu?: ReactNode;
}

/**
 * The header of every flow page, read only or editing, so the two look like
 * one place: back, the name and its state on the left, actions on the right.
 */
export function FlowHeader({ back, title, meta, actions, menu }: FlowHeaderProps) {
  const phone = useMediaQuery('(max-width: 47.99em)');

  return (
    <>
      <Group component="header" justify="space-between" align="center" wrap="nowrap" gap="sm" className="hermod-flow-header">
        <Group gap="sm" wrap="nowrap" style={{ flex: 1, minWidth: 0 }}>
          <Tooltip label={back.label} withArrow>
            <ActionIcon
              component={Link}
              to={back.to}
              params={back.params as any}
              aria-label={back.label}
              variant="default"
              size={phone ? 40 : 36}
              radius="md"
            >
              <IconChevronLeft size="1.1rem" aria-hidden />
            </ActionIcon>
          </Tooltip>
          <Stack gap={2} style={{ flex: 1, minWidth: 0 }}>
            {title}
            {meta && <Box className="hermod-flow-meta">{meta}</Box>}
          </Stack>
        </Group>
        <Group gap="xs" wrap="nowrap" style={{ flex: 'none' }}>
          {!phone && actions}
          {menu}
        </Group>
      </Group>
      {phone && actions && (
        <Box component="section" aria-label="Flow actions" className="hermod-flow-actions">
          {actions}
        </Box>
      )}
    </>
  );
}
