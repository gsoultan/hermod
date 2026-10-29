import { Box, Group, Text, ThemeIcon, UnstyledButton } from '@mantine/core';
import { IconChevronRight } from '@tabler/icons-react';
import { Link } from '@tanstack/react-router';
import type { ComponentType, ReactNode } from 'react';

interface RowProps {
  icon?: ComponentType<{ size?: string | number; stroke?: number }>;
  /** Colour the icon tile red for a row that needs attention. */
  iconTone?: 'default' | 'err';
  title: ReactNode;
  detail?: ReactNode;
  /** A StatePill, a rate, or a short word about where things stand. */
  state?: ReactNode;
  /** The one action: a switch, Retry, Fix, Approve. Everything else is behind the arrow. */
  action?: ReactNode;
  /** Where the row leads. Without it the row is static. */
  to?: string;
  params?: Record<string, string>;
  onOpen?: () => void;
  /** A checkbox for selecting the row, before the icon. */
  lead?: ReactNode;
}

/**
 * One row pattern everywhere: icon, name, one line of detail, state, action,
 * arrow. The name and detail are the link; the action sits beside it so
 * pressing Retry never also opens the row.
 */
export function Row({ icon: Icon, iconTone = 'default', title, detail, state, action, to, params, onOpen, lead }: RowProps) {
  const opens = Boolean(to || onOpen);
  const body = (
    <Group gap="md" wrap="nowrap" style={{ minWidth: 0, flex: 1 }}>
      {Icon && (
        <ThemeIcon
          variant="light"
          color={iconTone === 'err' ? 'red' : 'gray'}
          radius="md"
          size={36}
          aria-hidden
          className="hermod-row-icon"
          style={{ flex: 'none' }}
        >
          <Icon size="1.1rem" stroke={1.6} />
        </ThemeIcon>
      )}
      <Box style={{ minWidth: 0 }}>
        <Text fw={600} truncate="end" className="hermod-row-text">{title}</Text>
        {detail && <Text size="sm" c="dimmed" truncate="end" className="hermod-row-text">{detail}</Text>}
      </Box>
    </Group>
  );

  const main = to ? (
    <UnstyledButton component={Link} to={to} params={params as any} style={{ display: 'flex', minWidth: 0, flex: 1, alignSelf: 'stretch', alignItems: 'center' }}>
      {body}
    </UnstyledButton>
  ) : onOpen ? (
    <UnstyledButton onClick={onOpen} style={{ display: 'flex', minWidth: 0, flex: 1, alignSelf: 'stretch', alignItems: 'center' }}>
      {body}
    </UnstyledButton>
  ) : body;

  return (
    <Box
      component="li"
      px="md"
      style={{
        display: 'grid',
        alignItems: 'center',
        columnGap: 'var(--mantine-spacing-md)',
        minHeight: 64,
        paddingTop: 'var(--mantine-spacing-sm)',
        paddingBottom: 'var(--mantine-spacing-sm)',
        borderTop: '1px solid var(--mantine-color-default-border)',
      }}
      className={lead ? 'hermod-row hermod-row-with-lead' : 'hermod-row'}
    >
      {lead && <Box className="hermod-row-lead" onClick={(e) => e.stopPropagation()}>{lead}</Box>}
      <Box className="hermod-row-main" style={{ display: 'flex', minWidth: 0, alignSelf: 'stretch' }}>{main}</Box>
      {/* Its own grid cell: beside the name on a desktop, under it on a phone, where the name needs the width. */}
      {state && <Box className="hermod-row-state">{state}</Box>}
      <Group gap="md" wrap="nowrap" className="hermod-row-tail">
        {action && <Box onClick={(e) => e.stopPropagation()}>{action}</Box>}
        {opens && <IconChevronRight size="1rem" aria-hidden className="hermod-row-chevron" style={{ color: 'var(--mantine-color-dimmed)' }} />}
      </Group>
    </Box>
  );
}
