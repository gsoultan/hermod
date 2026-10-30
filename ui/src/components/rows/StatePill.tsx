import { Box, type BoxProps } from '@mantine/core';
import type { ReactNode } from 'react';

export type Tone = 'ok' | 'err' | 'warn' | 'off' | 'info';

const COLOR: Record<Tone, string> = {
  ok: 'var(--mantine-color-green-7)',
  err: 'var(--mantine-color-red-7)',
  warn: 'var(--mantine-color-orange-7)',
  off: 'var(--mantine-color-dimmed)',
  info: 'var(--mantine-color-indigo-6)',
};

interface StatePillProps extends BoxProps {
  tone: Tone;
  children: ReactNode;
}

/**
 * A dot and a few words. State colour is used for state only: green runs,
 * red fails, orange needs a look, grey is off.
 */
export function StatePill({ tone, children, ...rest }: StatePillProps) {
  return (
    <Box
      component="span"
      data-tone={tone}
      fz="sm"
      fw={600}
      style={{ display: 'inline-flex', alignItems: 'center', gap: 6, color: COLOR[tone], whiteSpace: 'nowrap' }}
      {...rest}
    >
      <Box component="i" aria-hidden style={{ width: 8, height: 8, borderRadius: '50%', background: 'currentColor', flex: 'none' }} />
      {children}
    </Box>
  );
}
