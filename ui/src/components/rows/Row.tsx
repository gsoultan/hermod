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
}

/**
 * One row pattern everywhere: icon, name, one line of detail, state, action,
 * arrow. The name and detail are the link; the action sits beside it so
 * pressing Retry never also opens the row.
 */
export function Row({ icon: Icon, iconTone = 'default', title, detail, state, action, to, params, onOpen }: RowProps) {
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
          style={{ flex: 'none' }}
        >
          <Icon size="1.1rem" stroke={1.6} />
        </ThemeIcon>
      )}
      <Box style={{ minWidth: 0 }}>
        <Text fw={600} truncate="end">{title}</Text>
        {detail && <Text size="sm" c="dimmed" truncate="end">{detail}</Text>}
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
        display: 'flex',
        alignItems: 'center',
        gap: 'var(--mantine-spacing-md)',
        minHeight: 64,
        paddingTop: 'var(--mantine-spacing-sm)',
        paddingBottom: 'var(--mantine-spacing-sm)',
        borderTop: '1px solid var(--mantine-color-default-border)',
      }}
      className="hermod-row"
    >
      {main}
      <Group gap="md" wrap="nowrap" style={{ flex: 'none' }}>
        {state}
        {action && <Box onClick={(e) => e.stopPropagation()}>{action}</Box>}
        {opens && <IconChevronRight size="1rem" aria-hidden style={{ color: 'var(--mantine-color-dimmed)' }} />}
      </Group>
    </Box>
  );
}
