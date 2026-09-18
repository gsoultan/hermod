import { test, expect, type Page } from '@playwright/test';
import { E2E_USER, E2E_PASS } from './support/auth';

/**
 * The set-fields row order, driven through the real editor.
 *
 * A unit test of SetFieldEditor already pins this, but that test supplies its
 * own `updateNodeConfig`. What the user actually types into goes through the
 * workflow store, the config drawer and a lazily-imported chunk, and a bug in
 * any of those is invisible to the unit test. Reported still broken after the
 * unit fix, so this starts where the user starts.
 */

const login = async (page: Page) => {
  await page.goto('/login');
  await page.getByPlaceholder('Your username').fill(E2E_USER);
  await page.getByPlaceholder('Your password').fill(E2E_PASS);
  await page.getByRole('button', { name: /sign in|login/i }).click();
  await page.waitForURL((u) => !u.pathname.startsWith('/login'), { timeout: 30000 });
};

const targetPaths = (page: Page) => page.getByRole('combobox', { name: 'Target path' });

const pathValues = async (page: Page) => {
  const n = await targetPaths(page).count();
  const out: string[] = [];
  for (let i = 0; i < n; i += 1) out.push(await targetPaths(page).nth(i).inputValue());
  return out;
};

test('typing in a set field leaves it where it is', async ({ page }) => {
  test.setTimeout(240000);
  await page.setViewportSize({ width: 1440, height: 900 });
  await login(page);

  await page.goto('/workflows/new', { waitUntil: 'networkidle' });
  await page.waitForTimeout(2000);

  if (!(await page.getByText('Workflow Panel').isVisible().catch(() => false))) {
    await page.getByRole('button', { name: 'Workflow panel' }).click();
  }
  await expect(page.getByText('Workflow Panel')).toBeVisible();

  // A source has to exist before the Transformations tab unlocks.
  await page.getByText('PostgreSQL', { exact: true }).first().click();
  await page.waitForTimeout(1500);
  await page.keyboard.press('Escape');
  await page.waitForTimeout(800);

  await page.getByRole('tab', { name: 'Transformations' }).click();
  await page.waitForTimeout(1000);
  await page.getByText('Set Fields', { exact: true }).first().click();
  await page.waitForTimeout(1500);

  const addField = page.getByRole('button', { name: /add new field mapping/i });
  await expect(addField, 'choosing Set Fields should open its editor').toBeVisible();

  for (let i = 0; i < 3; i += 1) {
    await addField.click();
    await page.waitForTimeout(400);
  }
  await expect(targetPaths(page)).toHaveCount(3);

  // Each row gets a distinct generated name, so a reorder is visible.
  const before = await pathValues(page);
  expect(new Set(before).size, `rows should have distinct names, got ${before}`).toBe(3);

  // Type into the *first* row. This is the reported bug: the row jumped to the
  // bottom on the first keystroke, and with rows keyed by position the caret was
  // left in whichever row moved up into it.
  //
  // The whole value is replaced rather than appended to. Where a keystroke lands
  // inside the text is a separate question -- Mantine's combobox owns Home/End
  // while its dropdown is open, so the caret stays where the click landed -- and
  // it would make the assertion about caret placement instead of about order.
  // Select-all then type still fires one rename per character, which is the case
  // that broke.
  await targetPaths(page).first().click();
  await page.keyboard.press('ControlOrMeta+a');
  await page.keyboard.type('user.id', { delay: 60 });
  await page.waitForTimeout(600);

  const after = await pathValues(page);
  expect(after, 'the edited row must stay first and the others must not move').toEqual([
    'user.id',
    before[1],
    before[2],
  ]);

  // And the caret must still be in the row that was being typed into, holding
  // the text that was typed -- not in a neighbour that slid up into position.
  const focused = await page.evaluate(() => {
    const el = document.activeElement as HTMLInputElement | null;
    return { label: el?.getAttribute('aria-label') ?? null, value: el?.value ?? null };
  });
  expect(focused).toEqual({ label: 'Target path', value: 'user.id' });
});
