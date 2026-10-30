import { Box, type BoxProps } from '@mantine/core';
import type { ReactNode } from 'react';

interface RowListProps extends BoxProps {
  'aria-label': string;
  children: ReactNode;
}

/** A bordered list of Rows. Lists, settings, activity and people all use it. */
export function RowList({ children, ...rest }: RowListProps) {
  return (
    <Box
      component="ul"
      m={0}
      p={0}
      style={{
        listStyle: 'none',
        border: '1px solid var(--mantine-color-default-border)',
        borderRadius: 12,
        overflow: 'hidden',
        background: 'var(--mantine-color-body)',
      }}
      {...rest}
    >
      {children}
    </Box>
  );
}
