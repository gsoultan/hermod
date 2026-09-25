import { test, expect, type Page } from '@playwright/test';
import { login, apiRequest } from './support/auth';

/**
 * A Data Conversion row converting to Date had one "Date Format" field, and it
 * only ever described how the value is *read*. An operator set it to
 * "02 January 2026" to get "18 September 2026" and Live Preview showed
 * 2026-09-18T04:30:57.333046Z unchanged -- the layout did not match the value,
 * the node read it as ISO 8601 instead, and nothing wrote with it. The layout
 * was mistyped too: Go spells every year 2006.
 *
 * This starts where that operator started -- a stored workflow whose source
 * sample carries the timestamp, and the row exactly as the old field left it --
 * and ends at what the engine returns to the preview. The unit tests pin each
 * side of the `outputFormat` key; this is the one place both sides meet.
 */

const createdSources: string[] = [];
const createdSinks: string[] = [];
const createdWorkflows: string[] = [];

test.afterEach(async ({ page }) => {
  for (const id of createdWorkflows.splice(0)) {
    await apiRequest(page, `/api/workflows/${id}`, { method: 'DELETE' }).catch(() => {});
  }
  for (const id of createdSinks.splice(0)) {
    await apiRequest(page, `/api/sinks/${id}`, { method: 'DELETE' }).catch(() => {});
  }
  for (const id of createdSources.splice(0)) {
    await apiRequest(page, `/api/sources/${id}`, { method: 'DELETE' }).catch(() => {});
  }
});

async function send(page: Page, method: string, path: string, body: unknown): Promise<any> {
  const res = await apiRequest(page, path, { method, body });
  if (res.status >= 400) {
    throw new Error(`fixture setup failed: ${method} ${path} -> ${res.status} ${res.body}`);
  }
  return res.body ? JSON.parse(res.body) : null;
}

async function dateConversionWorkflow(page: Page, stamp: number): Promise<string> {
  const source = await send(page, 'POST', '/api/sources', {
    name: `dateconv-src-${stamp}`, type: 'webhook', vhost: 'default', active: true,
    config: { path: `/dateconv-${stamp}` },
  });
  createdSources.push(source.id);
  await send(page, 'PUT', `/api/sources/${source.id}/sample`, {
    sample: JSON.stringify({ id: 1, created_at: '2026-09-18T04:30:57.333046Z' }),
  });

  const sink = await send(page, 'POST', '/api/sinks', {
    name: `dateconv-snk-${stamp}`, type: 'stdout', vhost: 'default', config: {},
  });
  createdSinks.push(sink.id);

  const workflow = await send(page, 'POST', '/api/workflows', {
    name: `dateconv-wf-${stamp}`, vhost: 'default', active: false,
    nodes: [
      { id: 'n-src', type: 'source', ref_id: source.id, x: 60, y: 160, config: { label: 'Orders' } },
      {
        id: 'n-conv', type: 'transformation', x: 360, y: 160,
        config: {
          label: 'Dates', transType: 'data_conversion',
          conversions: [{ field: 'created_at', targetType: 'date', format: '02 January 2026' }],
        },
      },
      { id: 'n-snk', type: 'sink', ref_id: sink.id, x: 660, y: 160, config: { label: 'Console' } },
    ],
    edges: [
      { id: 'e1', source_id: 'n-src', target_id: 'n-conv' },
      { id: 'e2', source_id: 'n-conv', target_id: 'n-snk' },
    ],
  });
  createdWorkflows.push(workflow.id);
  return workflow.id;
}

test('a date is written in the output format picked from the list', async ({ page }, testInfo) => {
  test.setTimeout(120000);
  await page.setViewportSize({ width: 1600, height: 1000 });
  await login(page);
  const id = await dateConversionWorkflow(page, Date.now());

  await page.goto(`/workflows/${id}/edit`);
  await expect(page.getByTestId('rf__node-n-conv')).toBeVisible({ timeout: 20000 });
  await page.getByTestId('rf__node-n-conv').dblclick();

  const row = page.getByTestId('conversion-row-0');
  const preview = page.getByTestId('live-preview');
  const outputFormat = row.getByRole('combobox', { name: 'Output format' });
  await expect(row).toBeVisible({ timeout: 20000 });

  // The report, as the stored row reproduces it: nothing chosen for the output,
  // so the timestamp comes back as it went in. The typo is called out where it
  // was typed.
  await expect(outputFormat).toHaveValue('Keep as a date/time value');
  await expect(row.getByRole('textbox', { name: 'Custom input layout' })).toHaveValue('02 January 2026');
  await expect(row.getByText(/“2026” is not a year in a Go layout/)).toBeVisible();
  await expect(preview).toContainText('"2026-09-18T04:30:57.333046Z"', { timeout: 20000 });

  // Chosen, not typed.
  await outputFormat.click();
  await page.getByRole('option', { name: /^18 September 2026\s*DD MMMM YYYY$/ }).click();
  await expect(outputFormat).toHaveValue('18 September 2026');
  await expect(preview, 'the engine did not write the date in the chosen format')
    .toContainText('"created_at": "18 September 2026"', { timeout: 20000 });

  // One click corrects the mistyped read layout to the listed format it meant.
  await row.getByRole('button', { name: /use 02 january 2006/i }).click();
  await expect(row.getByRole('combobox', { name: 'Input format' })).toHaveValue('18 September 2026');
  await expect(row.getByRole('textbox', { name: 'Custom input layout' })).toHaveCount(0);
  await expect(preview).toContainText('"created_at": "18 September 2026"', { timeout: 20000 });

  await page.screenshot({ path: testInfo.outputPath('date-conversion-format.png'), animations: 'disabled' });
});
