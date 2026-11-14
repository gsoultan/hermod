/**
 * Scenario C: every source type and every sink type the Add forms offer.
 *
 * For each type, through the real wizard at /sources/new and /sinks/new:
 *
 * C1  Basics: name, virtual host and type can be chosen; Next opens the
 *     connection step, which shows that type's own fields.
 * C2  The connection step names what it needs: while a required field is
 *     blank, Next is disabled. Each required field has a visible input under
 *     the label the "Required:" tooltip uses, and filling those makes Next work.
 * C3  Test Connection answers. Against a placeholder host that is a refusal
 *     (4xx with a message), never a server error or a crash.
 * C4  The rest of the wizard can be walked to the end and the connector saved;
 *     it is then listed, its edit page opens with the saved name, and it can
 *     be deleted.
 *
 * Every connector's form, fields and save path are covered this way. Whether a
 * connector actually reaches its external system (a Postgres replication slot,
 * a Kafka broker, an S3 bucket) is not: CI has none of them. The ones that run
 * on the same machine are delivered to live in live_e2e.spec.ts.
 *
 * The type lists are SOURCE_TYPES and SINK_TYPES, parsed from the forms (see
 * catalog.ts), and the required fields come from connectorRequirements.ts, the
 * module the wizard itself gates on -- so a new type is tested by the next run.
 */
import { test, expect, type Page } from '@playwright/test';
import { SINK_TYPES, SOURCE_TYPES, type ConnectorType } from './catalog';
import { apiRaw, expectNoErrorPage, login, settle, stamp, watchErrors } from './helpers';
import { missingConnectionFields, requirementsFor } from '../../src/lib/connectorRequirements';

type Kind = 'source' | 'sink';

const nextButton = (page: Page) => page.getByRole('button', { name: 'Next Step' });

async function chooseBasics(page: Page, kind: Kind, type: ConnectorType, name: string) {
  await page.goto(`/${kind}s/new`);
  await settle(page, 200);
  await page.getByRole('textbox', { name: 'Name' }).fill(name);
  await page.getByRole('combobox', { name: 'Virtual host' }).click();
  await page.getByRole('option', { name: 'default', exact: true }).click();
  await page.getByRole('combobox', { name: 'Type' }).click();
  await page.getByRole('option', { name: type.label, exact: true }).first().click();
  await expect(nextButton(page)).toBeEnabled();
  await nextButton(page).click();
  await expect(page.getByText(/Step 2/).first(), 'the connection step did not open').toBeVisible();
  // Each type's fields are a lazily loaded chunk.
  await expect(page.getByText('Loading config fields...'), 'the type\'s fields never loaded').toHaveCount(0, {
    timeout: 15000,
  });
}


/**
 * The fields the wizard requires for `type` before Next works, as
 * connectorRequirements.ts states them with an empty config. Labels only:
 * each must be a field on the page.
 */
function requiredLabels(kind: Kind, type: string) {
  return missingConnectionFields(kind, type, {});
}


/**
 * Types whose required setting is not a field to type into. The test stops
 * after checking that Next is gated, and says where the rest is covered.
 */
const BUILT_ELSEWHERE: Record<string, string> = {
  'source:batch_sql':
    'its queries are written in the SQL Query Builder over another database source, so this stops after C2; batch_sql_available_fields_e2e runs a saved batch_sql source',
};

/** A form field by its accessible name, whatever kind of input it is. */
function fieldNamed(page: Page, label: string) {
  const main = page.locator('main');
  // The requirement list and the field may differ in case ("Webhook path"
  // and "Webhook Path"); the words must be the same.
  const name = new RegExp(`^${label.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}$`, 'i');
  return main
    .getByRole('textbox', { name })
    .or(main.getByRole('combobox', { name }))
    .or(main.getByRole('spinbutton', { name }))
    .first();
}

/**
 * Fill each field the wizard requires, as connectorRequirements.ts lists it,
 * with that field's own example. Returns the labels no field on the page has.
 */
async function fillRequired(page: Page, kind: Kind, type: string): Promise<string[]> {
  const unfillable: string[] = [];
  // Re-read what is still missing after each field: some requirements apply
  // only once another field has a value.
  for (let round = 0; round < 8; round++) {
    const config = await currentConfigGuess(page, kind, type);
    const label = missingConnectionFields(kind, type, config).find((l) => !unfillable.includes(l));
    if (!label) break;
    // "Token or username" is one requirement met by either of two fields.
    let field = fieldNamed(page, label);
    for (const part of label.split(/ or /i)) {
      if (await field.isVisible().catch(() => false)) break;
      field = fieldNamed(page, part);
    }
    if (!(await field.isVisible().catch(() => false))) {
      unfillable.push(label);
      continue;
    }
    const example = onThisMachine(label, requirementsFor(kind, type).find((r) => r.label === label)?.example || 'e2e-value');
    // A free-text combobox (Autocomplete) takes typing; a pick-only Select is
    // read-only and takes an option.
    const readOnly = (await field.getAttribute('readonly').catch(() => null)) !== null;
    if (readOnly) {
      await field.click();
      await page.getByRole('option').first().click();
    } else {
      await field.fill(example);
      await page.keyboard.press('Escape');
    }
    filled.add(label);
    await page.waitForTimeout(100);
  }
  return unfillable;
}

/**
 * The field's example, pointed at a closed port on this machine. Test
 * Connection against a made-up hostname waits on a lookup that may never
 * answer offline; 127.0.0.1:1 is refused at once, which is the answer under
 * test (a refusal, shown, without a server error).
 */
function onThisMachine(label: string, example: string): string {
  if (/^port$/i.test(label)) return '1';
  return example
    .replace(/\b(?:[\w-]+\.)*example\.com\b/g, '127.0.0.1')
    .replace(/\b(?:node|broker)\d\b/g, '127.0.0.1')
    .replace(/@host\b/g, '@127.0.0.1')
    .replace(/127\.0\.0\.1:\d+/g, '127.0.0.1:1');
}

/** Labels this test has filled, standing in for the form's config. */
const filled = new Set<string>();
async function currentConfigGuess(_page: Page, kind: Kind, type: string) {
  const config: Record<string, unknown> = {};
  for (const r of requirementsFor(kind, type)) {
    if (filled.has(r.label)) config[r.key] = r.example || 'e2e-value';
  }
  return config;
}

/** What the disabled Next button's tooltip says is missing. */
async function requiredNow(page: Page): Promise<string> {
  if (await nextButton(page).isEnabled()) return '';
  await nextButton(page).locator('xpath=..').hover({ force: true }).catch(() => {});
  const text = (await page.getByRole('tooltip').textContent({ timeout: 2000 }).catch(() => '')) ?? '';
  await page.mouse.move(0, 0);
  return text;
}

/** Close every notification, so none covers the buttons under it. */
async function dismissNotifications(page: Page) {
  const close = page.locator('.mantine-Notification-root .mantine-Notification-closeButton');
  for (let i = 0; i < 10 && (await close.count()) > 0; i++) {
    await close.first().click({ force: true }).catch(() => {});
    await page.waitForTimeout(150);
  }
}

async function walkToEndAndSave(page: Page, kind: Kind) {
  const create = page.getByRole('button', { name: kind === 'source' ? 'Create Source' : 'Create Sink' });
  for (let i = 0; i < 4 && !(await create.isVisible().catch(() => false)); i++) {
    if (!(await nextButton(page).isVisible().catch(() => false))) break;
    await expect(nextButton(page), `a later ${kind} step cannot be left: ${await requiredNow(page)}`).toBeEnabled();
    await nextButton(page).click();
    await page.waitForTimeout(150);
  }
  await expect(create, 'the wizard has no save button at its last step').toBeVisible();
  const saved = page.waitForResponse(
    (r) => r.request().method() === 'POST' && new URL(r.url()).pathname === `/api/${kind}s`,
    { timeout: 15000 },
  );
  await create.click();
  return saved;
}

for (const kind of ['source', 'sink'] as const) {
  const types = kind === 'source' ? SOURCE_TYPES : SINK_TYPES;
  test.describe(`${kind}s: every type through the Add form`, () => {
    for (const type of types) {
      test(`${kind} › ${type.group} › ${type.label} (${type.value})`, async ({ page }) => {
        const watch = watchErrors(page);
        await login(page);
        const name = `cov-${kind}-${type.value}-${stamp()}`.slice(0, 60);
        let createdId: string | undefined;
        filled.clear();
        try {
          // C1
          await chooseBasics(page, kind, type, name);
          await expectNoErrorPage(page, `${type.label} connection step`);
          const fields = page.locator('main').locator('input:not([type="hidden"]), textarea, .cm-editor');
          // A type with nothing to set says so, rather than showing a blank step.
          if ((await fields.count()) === 0) {
            await expect(
              page.getByRole('alert', { name: 'Nothing to configure' }),
              'the connection step has no field and does not say why',
            ).toBeVisible();
          }

          // C2
          const required = requiredLabels(kind, type.value);
          if (required.length > 0) {
            await expect(nextButton(page), `Next works with ${required.join(', ')} blank`).toBeDisabled();
          }
          if (`${kind}:${type.value}` in BUILT_ELSEWHERE) {
            test.info().annotations.push({ type: 'scope', description: BUILT_ELSEWHERE[`${kind}:${type.value}`] });
            watch.expectClean(type.label);
            return;
          }
          const unfillable = await fillRequired(page, kind, type.value);
          expect(unfillable, `required by the wizard but no field on the page is labelled so`).toEqual([]);
          await expect(nextButton(page), `still missing: ${await requiredNow(page)}`).toBeEnabled();

          // C3
          const tested = page.waitForResponse((r) => r.url().endsWith(`/api/${kind}s/test`), { timeout: 45000 });
          await page.getByRole('button', { name: 'Test Connection' }).click();
          const res = await tested;
          expect(res.status(), `Test Connection failed on the server: ${await res.text()}`).toBeLessThan(500);
          await expectNoErrorPage(page, `${type.label} after Test Connection`);
          // The answer is shown on the step ("Connected" / "Connection Failed")
          // or as a notification; either way it must say something.
          const answer = page
            .getByRole('alert', { name: /^(Connected|Connection Failed)$/ })
            .or(page.locator('.mantine-Notification-root'))
            .last();
          await expect(answer, 'Test Connection showed no result').toBeVisible();
          expect((await answer.textContent())?.trim().length ?? 0).toBeGreaterThan(0);
          await dismissNotifications(page);

          // C4
          const saved = await walkToEndAndSave(page, kind);
          const body = await saved.text();
          expect(saved.status(), `saving failed on the server: ${body}`).toBeLessThan(500);
          expect(saved.status(), `saving was refused: ${body}`).toBeLessThan(300);
          createdId = JSON.parse(body).id;
          await expect(page, 'saving did not leave the wizard').not.toHaveURL(/\/new$/, { timeout: 10000 });

          const list = await apiRaw(page, 'GET', `/api/${kind}s?search=${encodeURIComponent(name)}`);
          expect(list.body, 'the saved connector is not listed').toContain(name);
          await page.goto(`/${kind}s/${createdId}/edit`);
          await expect(page.getByRole('textbox', { name: 'Name' })).toHaveValue(name);

          const del = await apiRaw(page, 'DELETE', `/api/${kind}s/${createdId}`);
          expect(del.status, `delete refused: ${del.body}`).toBeLessThan(300);
          createdId = undefined;
          watch.expectClean(type.label);
        } finally {
          if (createdId) await apiRaw(page, 'DELETE', `/api/${kind}s/${createdId}`);
        }
      });
    }
  });
}
