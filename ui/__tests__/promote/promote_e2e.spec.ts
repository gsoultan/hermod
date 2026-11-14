/**
 * Promote to production, end to end, across two real Hermods.
 *
 * Staging (STAGING_URL, on `localhost`) sends; production (PROD_URL,
 * on `127.0.0.1`) receives. Both are fresh instances, signed in as the
 * administrator first-run setup created, and the flow lives in the vhost `hr`.
 * See env.ts for why each of those matters.
 *
 * The scenarios build on each other and run in order, so the suite runs
 * serially and is not retried: a retry would start over against instances
 * the first attempt already changed. Each scenario is listed in
 * docs/testing/e2e-scenarios.md under D.
 */
import { test, expect, type Page } from '@playwright/test';
import { asUser, openBoth, PROD_URL, STAGING_URL, VHOST, type Instance } from './env';
import { Receiver } from '../coverage/helpers';

test.describe.configure({ mode: 'serial', retries: 0 });
test.setTimeout(180000);

let staging: Instance;
let prod: Instance;
/** A second administrator of production: its own "admin" has the sender's name. */
let approver: Instance;
const prodHost = new URL(PROD_URL).host;
/** Where the flow's crm sink delivers, on both instances. */
const crm = new Receiver();
let crmURL = '';

test.beforeAll(async ({ browser }) => {
  expect(new URL(STAGING_URL).hostname, 'staging must not share production\'s hostname (see env.ts)').not.toBe(new URL(PROD_URL).hostname);
  ({ staging, prod } = await openBoth(browser, STAGING_URL));
  crmURL = `${await crm.start()}/crm`;
});

test.afterAll(async () => crm.stop());

/** The environment badge beside the logo, which links to these settings. */
const badge = (page: Page, role: string) =>
  page.getByRole('navigation').first().getByRole('link', { name: role, exact: true });

/** An input by its label, whether Mantine renders it as a textbox or a combobox. */
const field = (within: Page | ReturnType<Page['getByRole']>, name: string) =>
  within.getByRole('textbox', { name, exact: true }).or(within.getByRole('combobox', { name, exact: true })).first();

async function settingsEnvironments(i: Instance) {
  await i.page.goto(i.url('/settings?tab=environments'));
  await expect(i.page.getByRole('heading', { name: 'Environments', exact: true })).toBeVisible({ timeout: 20000 });
}

async function setRole(i: Instance, role: 'Staging' | 'Production') {
  await settingsEnvironments(i);
  const page = i.page;
  await page.getByRole('radiogroup', { name: 'What this instance is' }).getByText(role, { exact: true }).click();
  await page.getByRole('textbox', { name: 'Name' }).fill(role);
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(page.getByText(`This instance is now ${role}.`)).toBeVisible();
}

test('D1: a fresh instance has no role; setting Staging and Production shows the header badge', async () => {
  for (const [i, role] of [[staging, 'Staging'], [prod, 'Production']] as const) {
    const self = await i.api('GET', '/api/environments/self');
    expect(self.role, `${i.name} is not a fresh instance: start it with --reset`).toBe('');
    await i.page.goto(i.url('/environments'));
    await expect(i.page.getByText('This instance has no environment role yet.')).toBeVisible();
    await expect(badge(i.page, role)).toHaveCount(0);

    await setRole(i, role);
    // The badge, always in view from then on, and it leads back here.
    await i.page.goto(i.url('/'));
    await expect(badge(i.page, role)).toBeVisible();
    await expect(badge(i.page, role)).toHaveAttribute('href', '/settings?tab=environments');
  }
});

/** Open Connect a server on staging and fill the first step for `from`. */
async function connectStep1(from: string) {
  const page = staging.page;
  await settingsEnvironments(staging);
  await page.getByRole('button', { name: 'Connect a server' }).click();
  const wizard = page.getByRole('dialog', { name: 'Connect an environment' });
  await wizard.getByRole('textbox', { name: 'Address' }).fill(PROD_URL);
  await field(wizard, 'Workflows from').click();
  await page.getByRole('option', { name: from, exact: true }).click();
  await expect(wizard.getByRole('textbox', { name: 'Land in' })).toHaveValue(from);
  await wizard.getByRole('button', { name: 'Next' }).click();
  await expect(wizard.getByRole('button', { name: `Sign in to ${prodHost} and approve` })).toBeVisible();
  return wizard;
}

test('D2: Connect a server, by signing in to production in a popup and by a pairing code', async () => {
  for (const name of [VHOST, 'payroll']) {
    const vh = await staging.raw('POST', '/api/vhosts', { name, description: 'promote suite' });
    expect(vh.status, vh.body).toBeLessThan(300);
  }

  // Sign in there and Allow: production opens in a popup, already signed in.
  let wizard = await connectStep1(VHOST);
  const popupOpened = staging.page.waitForEvent('popup');
  await wizard.getByRole('button', { name: `Sign in to ${prodHost} and approve` }).click();
  const popup = await popupOpened;
  await expect(popup).toHaveURL(new RegExp(`^${PROD_URL}/environments/accept/`));
  await expect(popup.getByText(`${new URL(STAGING_URL).origin}`, { exact: false }).first()).toBeVisible();
  // hr is not on production yet: allowing creates it.
  await popup.getByRole('button', { name: `Create ${VHOST} and allow` }).click();
  await expect(wizard.getByText('Connected to Production')).toBeVisible({ timeout: 20000 });
  await expect(wizard.getByText(`Workflows in ${VHOST} can now be promoted to Production, into ${VHOST}, which it created for them`)).toBeVisible();
  // Nothing is in hr yet, so there is nothing to match.
  await expect(wizard.getByText('This instance has no sources or sinks yet, so there is nothing to match.')).toBeVisible();
  await staging.page.keyboard.press('Escape');
  await expect(wizard).toBeHidden();
  const prodVHosts = await prod.api<any>('GET', '/api/vhosts');
  expect(JSON.stringify(prodVHosts), 'production did not create hr').toContain(`"name":"${VHOST}"`);

  // A pairing code instead: an administrator of production makes one for payroll.
  await settingsEnvironments(prod);
  await field(prod.page, 'Into virtual host').fill('payroll');
  await prod.page.keyboard.press('Escape');
  await prod.page.getByRole('button', { name: 'Create pairing code' }).click();
  const codeBox = prod.page.getByRole('alert').filter({ hasText: 'Pairing code' });
  await expect(codeBox).toContainText('Its workflows land in payroll');
  const code = (await codeBox.locator('[style*="letter-spacing"]').first().textContent())!.trim();
  expect(code).toMatch(/^[A-Z2-9]{4}-?[A-Z2-9]{4}$/);

  wizard = await connectStep1('payroll');
  // A wrong code is refused, and says so. Production answers 400; staging
  // passes its refusal on as 502, the linked instance refusing, not as its own
  // failure (writeErr in the environment handler).
  await wizard.getByRole('textbox', { name: 'Pairing code' }).fill('ZZZZ-ZZZZ');
  const refused = staging.page.waitForResponse((r) => r.url().endsWith('/api/environments/links') && r.request().method() === 'POST');
  await wizard.getByRole('button', { name: 'Link', exact: true }).click();
  expect((await refused).status()).toBe(502);
  await expect(wizard.getByRole('alert').filter({ hasText: 'Not linked' })).toContainText('the pairing code is wrong, already used or expired');
  // The right one links.
  await wizard.getByRole('textbox', { name: 'Pairing code' }).fill(code);
  await wizard.getByRole('button', { name: 'Link', exact: true }).click();
  await expect(wizard.getByText('Connected to Production')).toBeVisible({ timeout: 20000 });
  await staging.page.keyboard.press('Escape');

  // Both vhosts are on the one server card.
  await settingsEnvironments(staging);
  const card = staging.page.getByRole('region', { name: 'Production' });
  await expect(card.getByText(`${VHOST} → ${VHOST}`)).toBeVisible();
  await expect(card.getByText('payroll → payroll')).toBeVisible();
});

/** What the flow is made of, on staging. Filled by D3. */
const flow = { id: '', name: 'Onboarding', hook: '/api/webhooks/promote-signups' };

/**
 * The flow in hr: a webhook in, an HTTP sink whose headers read two secrets,
 * and a stdout sink production already has under the same name.
 */
async function buildFlow() {
  const src = await staging.api('POST', '/api/sources', {
    name: 'signups', type: 'webhook', vhost: VHOST, config: { path: flow.hook },
  });
  const sink = await staging.api('POST', '/api/sinks', {
    name: 'crm', type: 'http', vhost: VHOST,
    config: { url: crmURL, method: 'POST', headers: 'Authorization: Bearer {{secret.CRM_KEY}}, X-Welcome: {{secret.WELCOME_TOKEN}}' },
  });
  const audit = await staging.api('POST', '/api/sinks', { name: 'audit-log', type: 'stdout', vhost: VHOST, config: {} });
  // Production already has its own audit-log, so that one is matched, not carried.
  await prod.api('POST', '/api/sinks', { name: 'audit-log', type: 'stdout', vhost: VHOST, config: {} });
  const wf = await staging.api('POST', '/api/workflows', {
    name: flow.name,
    vhost: VHOST,
    nodes: [
      { id: 's', type: 'source', ref_id: src.id, x: 0, y: 0 },
      { id: 'k1', type: 'sink', ref_id: sink.id, x: 300, y: -80 },
      { id: 'k2', type: 'sink', ref_id: audit.id, x: 300, y: 80 },
    ],
    edges: [
      { id: 'e1', source_id: 's', target_id: 'k1' },
      { id: 'e2', source_id: 's', target_id: 'k2' },
    ],
  });
  flow.id = wf.id;
}

/** Save a change to the flow on staging, which makes its next version. */
async function editFlow(change: (wf: any) => void) {
  const wf = await staging.api('GET', `/api/workflows/${flow.id}`);
  change(wf);
  await staging.api('PUT', `/api/workflows/${flow.id}`, wf);
}

test('D3: Match connections: same name matches, the rest are Missing; search and filters', async () => {
  await buildFlow();
  await settingsEnvironments(staging);
  const page = staging.page;
  const card = page.getByRole('region', { name: 'Production' });
  await card.getByRole('button', { name: 'Match connections' }).first().click();
  const dialog = page.getByRole('dialog', { name: 'Connections in Production' });
  await expect(dialog.getByText('1 of 3', { exact: true })).toBeVisible({ timeout: 20000 });
  const rows = dialog.getByRole('list', { name: 'Connections matched in Production' }).locator('li');
  const row = (name: string) => dialog.locator(`li[data-name="${name}"]`);
  await expect(rows).toHaveCount(3);
  await expect(row('audit-log')).toContainText('Matched');
  await expect(row('audit-log').getByRole('combobox', { name: 'Match for audit-log' })).toHaveValue(/audit-log/);
  for (const missing of ['signups', 'crm']) {
    await expect(row(missing)).toContainText('Missing');
    await expect(row(missing).getByRole('link', { name: /Create it in Production/ })).toHaveAttribute('href', new RegExp(`^${PROD_URL}/(sources|sinks)/new$`));
  }

  // Search narrows the list, by name or type.
  const search = dialog.getByPlaceholder('Search connections');
  await search.fill('crm');
  await expect(rows).toHaveCount(1);
  await expect(row('crm')).toBeVisible();
  await search.fill('nothing-like-this');
  await expect(dialog.getByText('No connection matches “nothing-like-this”.')).toBeVisible();
  await search.fill('');

  // The filters count and split them.
  await dialog.getByText('To check (2)').click();
  await expect(rows).toHaveCount(2);
  await dialog.getByText('Matched (1)').click();
  await expect(rows).toHaveCount(1);
  await expect(row('audit-log')).toBeVisible();
  await dialog.getByText('All (3)').click();
  await expect(rows).toHaveCount(3);
  await dialog.getByRole('button', { name: 'Done' }).click();
  await expect(dialog).toBeHidden();
});

/** The strip under the flow's header: here and its version, then production and where it stands there. */
async function strip() {
  await staging.page.goto(staging.url(`/workflows/${flow.id}`));
  const s = staging.page.getByRole('region', { name: 'Staging and Production' });
  await expect(s).toBeVisible({ timeout: 20000 });
  return s;
}

async function openPromote() {
  const page = staging.page;
  await page.goto(staging.url(`/workflows/${flow.id}`));
  await page.getByRole('button', { name: 'Promote to Production' }).click();
  const dialog = page.getByRole('dialog', { name: `Promote ${flow.name}` });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByText(/^Ready to send\./)).toBeVisible({ timeout: 20000 });
  return dialog;
}

/** Send what the open Promote dialog holds, and close it at the sent screen. */
async function send(dialog: ReturnType<Page['getByRole']>, label = 'Send for approval') {
  const sent = staging.page.waitForResponse((r) => r.url().endsWith('/api/environments/releases') && r.request().method() === 'POST');
  await dialog.getByRole('button', { name: label }).click();
  const res = await sent;
  expect(res.status(), await res.text()).toBeLessThan(300);
  await expect(dialog.getByText(/^1 workflow: the release (waits for approval|is live) in Production · hr\./)).toBeVisible();
  await dialog.getByRole('button', { name: 'Close' }).click();
}

test('D4+D5: the strip says "not there yet"; the setup admin promotes from hr, carrying two connections and typing CRM_KEY', async () => {
  await expect(await strip()).toContainText('not there yet');

  const checked = staging.page.waitForResponse((r) => r.url().endsWith('/api/environments/releases/preview'));
  const dialog = await openPromote();
  // The checks run for the setup admin, whose vhosts are null. Before the fix
  // this was a 403, "you do not have access to vhost hr".
  const res = await checked;
  expect(res.status(), await res.text()).toBe(200);
  await expect(dialog.getByText('An administrator other than you approves it for Production.')).toBeVisible();
  await expect(dialog.getByText('new there')).toBeVisible();
  // Version 1, the one saved when the flow was created. Before the fix a flow
  // promoted before its first edit went out as "v0".
  await expect(dialog.getByText('v1', { exact: true })).toBeVisible();

  // signups and crm are not in production: they go with the release. audit-log is matched.
  // Each connection's card starts with its name, then "Source · type" or "Sink · type".
  const conn = (name: string) => dialog.locator('.mantine-Paper-root').filter({ hasText: new RegExp(`^${name}(Source|Sink) · `) });
  await expect(conn('signups')).toContainText('Created in Production');
  await expect(conn('crm')).toContainText('Created in Production');
  await expect(conn('audit-log')).toContainText("Uses Production's audit-log");

  // Both secrets are new to production. The sender types CRM_KEY and leaves
  // WELCOME_TOKEN to the approver.
  await expect(dialog.getByText('2 secrets are new to Production')).toBeVisible();
  await dialog.getByLabel('CRM_KEY', { exact: true }).fill('crm-key-for-production');
  await expect(dialog.getByText('1 secret value left for the approver.')).toBeVisible();
  await dialog.getByRole('textbox', { name: 'Note for the approver' }).fill('First release of onboarding');
  await send(dialog);

  // Sent and waiting: before the fix the strip said "Up to date" here.
  await expect(await strip()).toContainText('v1 waits for approval there');
});

/** Production's rule for who approves, set on its Settings › Environments. */
async function setApproval(label: string) {
  await settingsEnvironments(prod);
  const page = prod.page;
  await field(page, 'Who approves a promoted workflow').click();
  await page.getByRole('option', { name: label, exact: true }).click();
  await page.getByRole('button', { name: 'Save rules' }).click();
  await expect(page.getByText('Promotion rules saved.')).toBeVisible();
}

/** The Approvals entry in the sidebar; its name carries the count when something waits for you. */
const approvalsNav = (page: Page) => page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: /^Approvals/ });

/** Staging's row for the release on Approvals, under "Sent from here, waiting there". */
async function waitingOnStaging() {
  const page = staging.page;
  await page.goto(staging.url('/approvals?tab=releases'));
  await expect(page.getByRole('heading', { name: 'Sent from here, waiting there' })).toBeVisible({ timeout: 20000 });
  return page.getByText(`${flow.name} to Production`).locator('xpath=ancestor::div[contains(@class,"mantine-Group-root")][1]');
}

test('D6: on staging, the sender of a release sent under the default rule cannot approve it, and nothing counts it', async () => {
  const row = await waitingOnStaging();
  await expect(row.getByText('You sent this, so another administrator approves it.')).toBeVisible();
  await expect(row.getByRole('button', { name: `Approve ${flow.name}` })).toBeDisabled();
  // Nothing waits for this person, so the sidebar counts nothing. Before the
  // fix it counted the release they could not approve.
  await expect(approvalsNav(staging.page)).toHaveAccessibleName('Approvals');
});

/** Production's card for the waiting release, on Approvals. */
async function waitingOnProd(i: Instance) {
  await i.page.goto(i.url('/approvals?tab=releases'));
  await expect(i.page.getByRole('heading', { name: 'Sent here, waiting to go live' })).toBeVisible({ timeout: 20000 });
  return i.page.locator('.mantine-Paper-root').filter({ hasText: `Staging → ${VHOST}` }).first();
}

test('D7: production approves: the sender cannot, another admin fills the secret box, keeps it stopped, and it is Live with Undo', async ({ browser }) => {
  // Production's own "admin" has the sender's name, so it cannot approve
  // either (D9's "a production admin who also sent it").
  let card = await waitingOnProd(prod);
  await expect(card.getByText('You sent this, so another administrator approves it.')).toBeVisible();
  await expect(card.getByRole('button', { name: 'Approve and go live' })).toBeDisabled();
  await expect(approvalsNav(prod.page)).toHaveAccessibleName('Approvals');

  approver = await asUser(browser, prod, PROD_URL, { username: 'prod-approver', role: 'Administrator', vhosts: [] });
  await expect(approvalsNav(approver.page)).toHaveAccessibleName(/^Approvals,? 1 waiting/, { timeout: 70000 });
  card = await waitingOnProd(approver);
  await expect(card).toContainText('“First release of onboarding”');
  // One change, said once: not "1 changes".
  await expect(card.getByText('1 change', { exact: true })).toBeVisible();
  await expect(card).not.toContainText('1 changes');
  await expect(card).toContainText(`Approving creates source signups and sink crm in ${VHOST}.`);
  await expect(card).toContainText('admin typed values for CRM_KEY.');
  // The secret the sender left empty.
  await expect(card.getByText(`1 secret has no value in ${VHOST} yet`)).toBeVisible();
  await expect(card.getByLabel('WELCOME_TOKEN', { exact: true })).toBeVisible();
  const keep = card.getByRole('radio', { name: 'Keep it stopped until WELCOME_TOKEN has a value' });
  await expect(keep).toBeChecked();
  await expect(card.getByRole('radio', { name: 'Start it anyway (a secret manager serves them)' })).not.toBeChecked();

  await card.getByRole('button', { name: 'Approve and go live' }).click();
  const confirm = approver.page.getByRole('dialog', { name: `Approve ${flow.name}` });
  await expect(confirm).toContainText('stays stopped until WELCOME_TOKEN has a value');
  await confirm.getByRole('button', { name: 'Approve and go live' }).click();
  await expect(approver.page.getByText(`${flow.name} is live.`)).toBeVisible({ timeout: 30000 });

  // Releases on production: Live, kept stopped for the missing secret, and it can be undone.
  await approver.page.goto(approver.url('/environments'));
  const live = approver.page.getByRole('heading', { name: 'Recently live' }).locator('xpath=..');
  const row = live.locator('.mantine-Group-root').filter({ hasText: flow.name }).first();
  await expect(row.getByText('Live', { exact: true })).toBeVisible({ timeout: 20000 });
  await expect(row.getByRole('button', { name: 'Undo' })).toBeVisible();
  await expect(row).toContainText('Kept stopped until WELCOME_TOKEN has a value.');
  const there = await prod.api('GET', `/api/workflows/${flow.id}`);
  expect(there.active, 'deployed started although WELCOME_TOKEN has no value').toBe(false);
  const secrets = JSON.stringify(await prod.api('GET', `/api/vhosts/${VHOST}/secrets`));
  expect(secrets).toContain('CRM_KEY');
  expect(secrets, 'a secret value was listed').not.toContain('crm-key-for-production');

  // Staging sees it is there.
  await expect(await strip()).toContainText('Up to date');
  // Give WELCOME_TOKEN a value in production, so later versions start.
  await prod.api('PUT', `/api/vhosts/${VHOST}/secrets/WELCOME_TOKEN`, { value: 'welcome-for-production' });
});

test('D6b: with any_admin and decisions allowed from staging, the sender approves from staging and it goes live', async () => {
  await setApproval('Any administrator approves, the sender included');
  await editFlow((wf) => { wf.nodes[0].config = { ...(wf.nodes[0].config ?? {}), label: 'New signups' }; });
  await expect(await strip()).toContainText('last sent v1 · 1 version to promote');
  const dialog = await openPromote();
  await expect(dialog.getByText('An administrator approves it for Production.')).toBeVisible();
  await expect(dialog.getByText('now runs v1')).toBeVisible();
  await send(dialog);

  const row = await waitingOnStaging();
  const approve = row.getByRole('button', { name: `Approve ${flow.name}` });
  await expect(approve).toBeEnabled();
  await expect(row.getByText('You sent this, so another administrator approves it.')).toHaveCount(0);
  await expect(approvalsNav(staging.page)).toHaveAccessibleName(/^Approvals,? 1 waiting/, { timeout: 70000 });
  await approve.click();
  await staging.page.getByRole('dialog', { name: `Approve ${flow.name}` }).getByRole('button', { name: 'Approve and go live' }).click();
  await expect(staging.page.getByText(`${flow.name} is live in Production.`)).toBeVisible({ timeout: 30000 });
  await expect(await strip()).toContainText('Up to date');
  await expect.poll(async () => (await prod.api('GET', `/api/workflows/${flow.id}`)).active, { timeout: 30000 }).toBe(true);
  await setApproval('An administrator other than the sender approves');
});

test('D7b: production rejects a release with a reason, and both sides keep it', async () => {
  await editFlow((wf) => { wf.nodes[1].config = { ...(wf.nodes[1].config ?? {}), label: 'CRM' }; });
  await send(await openPromote());

  const card = await waitingOnProd(approver);
  await card.getByRole('button', { name: 'Reject' }).click();
  const why = approver.page.getByRole('dialog', { name: `Reject ${flow.name}` });
  await why.getByRole('textbox', { name: 'Why' }).fill('Not during payroll week');
  await why.getByRole('button', { name: 'Reject' }).click();
  await expect(why).toBeHidden();

  await approver.page.goto(approver.url('/environments'));
  const rejected = approver.page.locator('.mantine-Group-root').filter({ hasText: 'Not during payroll week' }).first();
  await expect(rejected.getByText('Rejected', { exact: true })).toBeVisible({ timeout: 20000 });

  await staging.page.goto(staging.url('/environments'));
  const sent = staging.page.getByRole('heading', { name: 'Recently sent' }).locator('xpath=..');
  await expect(sent.locator('.mantine-Group-root').filter({ hasText: 'Not during payroll week' }).first()).toContainText('Rejected');
  // Rejected, so the version is still to promote.
  await expect(await strip()).toContainText('last sent v2');
});

test('D8: a release that starts failing in production is undone on its own, there and in staging\'s list', async () => {
  // The next version also delivers to a server nobody listens on.
  const broken = await staging.api('POST', '/api/sinks', {
    name: 'crm-backup', type: 'http', vhost: VHOST, config: { url: 'http://127.0.0.1:9/down', method: 'POST' },
  });
  await editFlow((wf) => {
    wf.nodes.push({ id: 'k3', type: 'sink', ref_id: broken.id, x: 300, y: 240 });
    wf.edges.push({ id: 'e3', source_id: 's', target_id: 'k3' });
  });
  const dialog = await openPromote();
  await expect(dialog.getByText('Undo the whole release if one of them starts failing')).toBeAttached();
  await send(dialog);

  const card = await waitingOnProd(approver);
  await expect(card.getByText('Undone on its own if it fails in the first 10 minutes')).toBeVisible();
  await card.getByRole('button', { name: 'Approve and go live' }).click();
  await approver.page.getByRole('dialog', { name: `Approve ${flow.name}` }).getByRole('button', { name: 'Approve and go live' }).click();
  await expect(approver.page.getByText(`${flow.name} is live.`)).toBeVisible({ timeout: 30000 });

  // Records that cannot be delivered count as failures too, in case the
  // engine keeps retrying the sink instead of stopping.
  for (let n = 0; n < 15; n++) {
    await prod.page.request.post(prod.url(flow.hook), { data: { n } }).catch(() => {});
  }
  // The watch looks every 30 seconds.
  await expect
    .poll(async () => (await prod.api<any[]>('GET', '/api/environments/promotions'))[0]?.status, {
      message: 'the failing release was not undone', timeout: 150000, intervals: [5000],
    })
    .toBe('rolled_back');
  await approver.page.goto(approver.url('/environments'));
  await expect(approver.page.locator('.mantine-Group-root').filter({ hasText: 'Rolled back automatically' }).first()).toContainText('Undone');
  await staging.page.goto(staging.url('/environments'));
  await expect(staging.page.getByRole('heading', { name: 'Recently sent' }).locator('xpath=..')).toContainText('Undone', { timeout: 20000 });
  // Before the fix the strip said "Up to date" after an undo.
  await expect(await strip()).toContainText('was undone there');
});

test('D9: who may do what: an editor outside hr, a viewer, and an editor who is not an administrator', async ({ browser }) => {
  const outsider = await asUser(browser, staging, STAGING_URL, { username: 'editor-payroll', role: 'Editor', vhosts: ['payroll'] });
  const item = [{ workflow_id: flow.id, version: 0 }];
  const preview = await outsider.raw('POST', '/api/environments/releases/preview', { items: item });
  expect(preview.status, `an editor without hr previewed it: ${preview.body}`).toBe(403);
  const sendIt = await outsider.raw('POST', '/api/environments/releases', { items: item });
  expect(sendIt.status, `an editor without hr sent it: ${sendIt.body}`).toBe(403);

  const viewer = await asUser(browser, staging, STAGING_URL, { username: 'viewer-hr', role: 'Viewer', vhosts: [VHOST] });
  await viewer.page.goto(viewer.url(`/workflows/${flow.id}`));
  await expect(viewer.page.getByRole('heading', { name: flow.name }).first()).toBeVisible({ timeout: 20000 });
  await expect(viewer.page.getByRole('button', { name: /^Promote/ })).toHaveCount(0);
  await viewer.page.goto(viewer.url('/environments'));
  await expect(viewer.page.getByText('Editors and administrators promote workflows from here.')).toBeVisible();
  const viewerSend = await viewer.raw('POST', '/api/environments/releases', { items: item });
  expect(viewerSend.status, viewerSend.body).toBe(403);

  // Linking is for administrators, on either side.
  const editor = await asUser(browser, staging, STAGING_URL, { username: 'editor-hr', role: 'Editor', vhosts: [VHOST] });
  const link = await editor.raw('POST', '/api/environments/links', { url: PROD_URL, code: 'ABCD-EFGH', vhost: VHOST });
  expect(link.status, link.body).toBe(403);
  const code = await editor.raw('POST', '/api/environments/pairing-codes', { label: 'x', vhost: VHOST });
  expect(code.status, code.body).toBe(403);
  const prodEditor = await asUser(browser, prod, PROD_URL, { username: 'prod-editor', role: 'Editor', vhosts: [VHOST] });
  const prodCode = await prodEditor.raw('POST', '/api/environments/pairing-codes', { label: 'x', vhost: VHOST });
  expect(prodCode.status, prodCode.body).toBe(403);
});

test('D10: Releases lanes per vhost: untick one and "Promote N-1 of N"; Add vhosts to the connected server', async () => {
  // Three new flows: two in hr, one in payroll.
  for (const [vhost, name] of [[VHOST, 'Offboarding'], [VHOST, 'Payslips notice'], ['payroll', 'Payslips']] as const) {
    const slug = name.replace(/\W+/g, '-').toLowerCase();
    const src = await staging.api('POST', '/api/sources', { name: `${slug}-in`, type: 'webhook', vhost, config: { path: `/api/webhooks/promote-${slug}` } });
    const out = await staging.api('POST', '/api/sinks', { name: `${slug}-log`, type: 'stdout', vhost, config: {} });
    await staging.api('POST', '/api/workflows', {
      name, vhost,
      nodes: [{ id: 's', type: 'source', ref_id: src.id, x: 0, y: 0 }, { id: 'k', type: 'sink', ref_id: out.id, x: 300, y: 0 }],
      edges: [{ id: 'e', source_id: 's', target_id: 'k' }],
    });
  }

  const page = staging.page;
  await page.goto(staging.url('/environments'));
  await expect(page.getByRole('heading', { name: 'Ready to promote' })).toBeVisible({ timeout: 20000 });
  const lane = (vhost: string) => page.locator('.mantine-Paper-root').filter({ hasText: `${vhost} → Production · ${vhost}` }).first();
  await expect(lane(VHOST).getByRole('checkbox', { name: 'Include Offboarding' })).toBeChecked();
  await expect(lane(VHOST).getByRole('checkbox', { name: 'Include Payslips notice' })).toBeChecked();
  await expect(lane('payroll').getByRole('checkbox', { name: 'Include Payslips' })).toBeChecked();
  // Onboarding was undone in production (D8), so its newest version is ready again.
  const all = await page.getByRole('button', { name: /^Promote all ready \((\d+)\)$/ }).textContent();
  const total = Number(all!.match(/\((\d+)\)/)![1]);
  expect(total).toBeGreaterThanOrEqual(3);

  await lane(VHOST).getByRole('checkbox', { name: 'Include Payslips notice' }).uncheck();
  const promote = page.getByRole('button', { name: `Promote ${total - 1} of ${total}` });
  await expect(promote).toBeVisible();
  await promote.click();
  const dialog = page.getByRole('dialog', { name: 'Promote' });
  await expect(dialog.getByText(new RegExp(`${total - 1} flows`)).first()).toBeVisible({ timeout: 20000 });
  await expect(dialog).not.toContainText('Payslips notice');
  await expect(dialog).toContainText('Payslips');
  await dialog.getByRole('button', { name: 'Cancel' }).click();

  // A third vhost goes to the same server without a new pairing code.
  await staging.api('POST', '/api/vhosts', { name: 'finance', description: 'promote suite' });
  await settingsEnvironments(staging);
  await page.getByRole('button', { name: 'Add vhosts to Production' }).click();
  const add = page.getByRole('dialog', { name: 'Add vhosts to Production' });
  await add.getByRole('checkbox', { name: 'finance' }).check();
  await add.getByRole('button', { name: 'Add 1 vhost' }).click();
  await expect(add.getByText('1 connected')).toBeVisible({ timeout: 20000 });
  await add.getByRole('button', { name: 'Done' }).click();
  await expect(page.getByRole('region', { name: 'Production' }).getByText('finance → finance')).toBeVisible();
  expect(JSON.stringify(await prod.api('GET', '/api/vhosts')), 'production did not create finance').toContain('"name":"finance"');
});

test('D11: guessing pairing codes: ten wrong ones void the outstanding codes, and too many attempts are refused', async () => {
  // Last on purpose: the attempt limit is per address, and both instances are on this machine.
  const fresh = await prod.api('POST', '/api/environments/pairing-codes', { label: 'guess test', vhost: 'guess' });
  const pair = (code: string) =>
    prod.page.evaluate(async (c) => {
      const r = await fetch('/api/environments/pair', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ code: c, name: 'Guesser', url: 'http://guess.invalid' }) });
      return r.status;
    }, code);
  for (let n = 0; n < 10; n++) expect(await pair('ZZZZ-ZZZZ')).toBe(400);
  // The right code no longer works: the run of wrong ones cleared it.
  expect(await pair(fresh.code), 'a valid code survived ten wrong guesses').toBe(400);
  let status = 0;
  for (let n = 0; n < 40 && status !== 429; n++) status = await pair('ZZZZ-ZZZZ');
  expect(status, 'pairing attempts were never limited').toBe(429);
});
