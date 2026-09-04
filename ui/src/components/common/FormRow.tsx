import { SimpleGrid } from '@mantine/core';
import type { ReactNode } from 'react';

/**
 * One row of form fields.
 *
 * Hermod's entity forms had three different ways of putting fields side by side
 * — `SimpleGrid`, bare stacked fields, and `<Group grow>` — so the same
 * conceptual form looked different on every page, and a measured audit of the
 * six main forms found five different width grammars (sources 928/456/456/456,
 * sinks 974 across the board, users 1016 down a single column, and so on).
 *
 * `<Group grow>` was the actively broken one: Group is a flex *row* that never
 * wraps, so on a narrow viewport it squeezed host/port/user/password to ~80px
 * each instead of stacking them. SimpleGrid collapses to one column, which is
 * what a form needs.
 *
 * The width grammar this encodes:
 *
 *   cols={1}  identifiers and free text — name, description, connection string
 *   cols={2}  paired settings          — host+port, user+password, vhost+worker
 *   cols={3}  enums and short numerics — ssl mode, retries, timeout
 *
 * Fields are bottom-aligned (see `.hermod-form-row` in index.css) so that a
 * two-line description on one field does not shunt its neighbour's input out of
 * line. That replaces a global `min-height: 1.2em` patch on every description in
 * the app, which only ever covered the single-line case.
 */
export function FormRow({
  children,
  cols = 2,
  align = 'end',
}: {
  children: ReactNode;
  /** Fields per row at the widest breakpoint. Always 1 on mobile. */
  cols?: 1 | 2 | 3;
  /**
   * `end` bottom-aligns the fields so their inputs line up regardless of
   * description height. Use `start` when the row holds something that is not a
   * field (a checkbox stack, say) and should hang from the top.
   */
  align?: 'start' | 'end';
}) {
  if (cols === 1) {
    return <>{children}</>;
  }

  return (
    <SimpleGrid
      cols={cols === 3 ? { base: 1, sm: 2, lg: 3 } : { base: 1, sm: 2 }}
      spacing="md"
      verticalSpacing="sm"
      className="hermod-form-row"
      style={{ alignItems: align }}
    >
      {children}
    </SimpleGrid>
  );
}
