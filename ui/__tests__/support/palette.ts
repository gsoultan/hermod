import type { Page } from '@playwright/test';

/**
 * A node in the workflow panel's palette, looked up inside the tab that shows it.
 *
 * Every tab of the panel stays mounted, and the Transformations tab lists every
 * node category -- sources and sinks included -- ahead of the Sources tab in the
 * DOM. So `page.getByText('PostgreSQL', { exact: true }).first()` resolved to a
 * hidden copy in the inactive Transformations tab, and the click waited out its
 * timeout on "element is not visible". Whether a run hit it depended on how far
 * the page had rendered when the locator resolved, which is why a spec could
 * pass on a CI runner and fail on a laptop. A role query skips hidden panels.
 */
export const paletteItem = (page: Page, tab: 'Sources' | 'Transformations' | 'Sinks', label: string) =>
  page.getByRole('tabpanel', { name: tab }).getByText(label, { exact: true }).first();
