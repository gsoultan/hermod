/**
 * Scenario F: every expression function gives the engine's documented answer
 * in every place a function can be written -- a Set Fields value (bare and as a
 * {{ }} token), an If condition, a Switch, and a Filter -- and the editor's
 * function picker offers every one of them.
 *
 * The functions, the sample record and the expected answers all come from
 * ui/src/lib/expressionFunctions.json, which the engine's own test holds to
 * what the engine runs (pkg/infra/evaluator/function_catalog_test.go). This
 * suite adds the part that test cannot see: the HTTP preview the editor calls,
 * and the condition operators on top of a function's result.
 *
 * It goes through the API the Live Preview and Test buttons call, with the
 * browser's session, rather than through a drawer per function: 75 functions
 * x 5 placements through the UI would take most of an hour and test the
 * drawer 375 times. The drawer itself is covered in conditions_e2e and
 * palette_e2e.
 */
import { test, expect, type Page } from '@playwright/test';
import { FUNCTIONS, FUNCTION_SAMPLE, type ExpressionFunction } from './catalog';
import { apiRaw, login } from './helpers';

/** How a condition renders a value to text before comparing (condition_value_shapes memory). */
function asConditionText(v: unknown): string {
  if (v === null || v === undefined) return '';
  if (typeof v === 'string') return v;
  if (typeof v !== 'object') return String(v);
  const sortKeys = (x: any): any =>
    Array.isArray(x)
      ? x.map(sortKeys)
      : x && typeof x === 'object'
        ? Object.fromEntries(Object.keys(x).sort().map((k) => [k, sortKeys(x[k])]))
        : x;
  return JSON.stringify(sortKeys(v));
}

async function preview(page: Page, type: string, config: Record<string, unknown>) {
  const res = await apiRaw(page, 'POST', '/api/transformations/test', {
    transformation: { type, config },
    message: FUNCTION_SAMPLE,
    vhost: 'default',
  });
  expect(res.status, `preview of a ${type} node failed: ${res.body}`).toBeLessThan(500);
  return { status: res.status, body: res.body ? JSON.parse(res.body) : null };
}

const fixed = FUNCTIONS.filter((f) => !f.volatile && f.result !== undefined);
const volatile = FUNCTIONS.filter((f) => f.volatile || f.result === undefined);

test.describe('every function, everywhere a function can be written', () => {
  test.beforeEach(async ({ page }) => login(page));

  for (const fn of fixed) {
    test(`${fn.category} › ${fn.name}: ${fn.example}`, async ({ page }) => {
      const want = fn.result;

      // Set Fields, bare expression.
      const set = await preview(page, 'set', { transType: 'set', 'column.out': fn.example });
      expect(set.body?.error, 'Set Fields refused it').toBeUndefined();
      expect(set.body.out, 'Set Fields (bare)').toEqual(want);

      // Set Fields, as a template token. A token renders into text when it
      // shares the value with other text, but a value that is only the token
      // keeps the result's type.
      const token = await preview(page, 'set', { transType: 'set', 'column.out': `{{ ${fn.example} }}` });
      expect(asConditionText(token.body.out), 'Set Fields ({{ }} token)').toEqual(asConditionText(want));

      // If: holds when compared with its own answer, and not with another.
      const holds = await preview(page, 'condition', {
        conditions: [{ field: fn.example, operator: '=', value: asConditionText(want) }],
      });
      expect(holds.body.branch, `If ${fn.example} = ${asConditionText(want)}`).toBe('true');
      const misses = await preview(page, 'condition', {
        conditions: [{ field: fn.example, operator: '=', value: '__never-this__' }],
      });
      expect(misses.body.branch, `If ${fn.example} = something else`).toBe('false');

      // Switch: the case naming the answer is the one taken.
      const sw = await preview(page, 'switch', {
        field: fn.example,
        cases: [
          { label: 'other', operator: '=', value: '__never-this__' },
          { label: 'hit', operator: '=', value: asConditionText(want) },
        ],
      });
      expect(sw.body.branch, `Switch on ${fn.example}`).toBe('hit');

      // Filter, the way the editor saves it (a JSON string): kept on a match.
      const kept = await preview(page, 'filter_data', {
        transType: 'filter_data',
        conditions: JSON.stringify([{ field: fn.example, operator: '=', value: asConditionText(want) }]),
      });
      expect(kept.body, `Filter on ${fn.example} dropped a matching record`).not.toBeNull();
    });
  }

  for (const fn of volatile) {
    test(`${fn.category} › ${fn.name} (no fixed answer): ${fn.example}`, async ({ page }) => {
      // secret() and hmac_sha256() read a vhost secret; give the example's one a value.
      const secretName = /^(secret|hmac_sha256)\(/.test(fn.example) ? fn.example.match(/"([A-Z0-9_]+)"/)?.[1] : undefined;
      if (secretName) {
        await apiRaw(page, 'PUT', `/api/vhosts/default/secrets/${secretName}`, { value: 'coverage-secret' });
      }
      try {
        await checkVolatile(page, fn);
      } finally {
        if (secretName) await apiRaw(page, 'DELETE', `/api/vhosts/default/secrets/${secretName}`);
      }
    });
  }
});

async function checkVolatile(page: Page, fn: ExpressionFunction) {
  const set = await preview(page, 'set', { transType: 'set', 'column.out': fn.example });
  expect(set.body?.error, 'Set Fields refused it').toBeUndefined();
  expect(set.body.out, `${fn.example} gave nothing`).not.toBeNull();
  expect(set.body.out).not.toBeUndefined();
}

/**
 * Comparisons on a function's result: the shape of Gembit's report,
 * `array_len(source.users) > 0`, across every ordering operator and the
 * neighbouring numeric functions.
 */
test.describe('If on a function result, every operator', () => {
  test.beforeEach(async ({ page }) => login(page));

  const cases: Array<[field: string, op: string, value: string, branch: 'true' | 'false']> = [
    ['array_len(source.tags)', '>', '0', 'true'],
    ['array_len(source.tags)', '>=', '2', 'true'],
    ['array_len(source.tags)', '<', '2', 'false'],
    ['array_len(source.tags)', '<=', '1', 'false'],
    ['array_len(source.tags)', '=', '2', 'true'],
    ['array_len(source.tags)', '!=', '2', 'false'],
    ['len(source.first_name)', '>', '2', 'true'],
    ['round(source.rating, 1)', '>=', '4.7', 'true'],
    ['abs(source.balance)', '>', '42', 'true'],
    ['lower(source.email)', 'contains', '@example.com', 'true'],
    ['upper(source.sku)', 'regex', '^TSHIRT-', 'true'],
    ['trim(source.city)', '=', 'Jakarta', 'true'],
    // The value side can call functions too.
    ['array_len(source.tags)', '=', '{{ array_len(source.tags) }}', 'true'],
    // A missing list has no length: null, which orders below nothing.
    ['array_len(source.missing)', '>', '0', 'false'],
  ];
  for (const [field, op, value, branch] of cases) {
    test(`${field} ${op} ${value} → ${branch}`, async ({ page }) => {
      const res = await preview(page, 'condition', { conditions: [{ field, operator: op, value }] });
      expect(res.body.branch).toBe(branch);
    });
  }
});

test('the function picker offers every function in the catalog', async ({ page }) => {
  await login(page);
  await page.goto('/workflows/new');
  // The picker is the same component everywhere; an If node's Field is the
  // quickest place to reach it.
  if (!(await page.getByText('Add a step', { exact: true }).isVisible().catch(() => false))) {
    await page.getByRole('button', { name: 'Add step' }).click();
    await page.getByRole('button', { name: 'Browse the full palette' }).click();
  }
  await page.getByRole('tabpanel', { name: 'Sources' }).getByText('Webhook', { exact: true }).first().click();
  await page.keyboard.press('Escape');
  if (!(await page.getByText('Add a step', { exact: true }).isVisible().catch(() => false))) {
    await page.getByRole('button', { name: 'Add step' }).click();
    await page.getByRole('button', { name: 'Browse the full palette' }).click();
  }
  await page.getByRole('tab', { name: 'Transformations' }).click();
  await page.getByRole('tabpanel', { name: 'Transformations' }).getByText('Condition (If)', { exact: true }).first().click();
  const drawer = page.getByRole('dialog').last();
  await drawer.getByRole('button', { name: 'Add Filter Condition' }).click();
  await drawer.getByRole('button', { name: 'Insert function' }).first().click();
  const search = page.getByRole('textbox', { name: 'Search functions' });
  const missing: string[] = [];
  for (const fn of FUNCTIONS as ExpressionFunction[]) {
    await search.fill(fn.name);
    const offered = page.getByRole('button', { name: `Insert ${fn.signature}`, exact: true });
    if (!(await offered.isVisible().catch(() => false))) missing.push(fn.signature);
  }
  expect(missing, 'the picker does not offer these').toEqual([]);
});
