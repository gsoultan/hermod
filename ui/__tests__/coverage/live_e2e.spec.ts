/**
 * Scenario L: connectors that run on this machine, with records really moving.
 *
 * Every source and sink form is covered in connectors_e2e; this spec runs the
 * ones that need no outside service as live workflows and checks what came
 * out the other end:
 *
 * L1  Form source -> HTTP sink.
 * L2  Cron source -> HTTP sink: the schedule fires and its payload arrives.
 * L3  HTTP polling source -> HTTP sink: what the polled URL serves arrives.
 * L4  Webhook source -> File sink: the record is appended to the file.
 * L5  Webhook source -> SQLite sink: the row is in the table.
 * L6  Webhook source -> Stdout sink: the workflow runs and counts the record.
 * L7  Webhook -> Filter (conditions stored as a list, the API's shape) -> HTTP
 *     sink: only the matching record arrives.
 *
 * Postgres CDC, MySQL, Kafka, RabbitMQ, Redis, S3, SaaS APIs and the rest need
 * their own servers or accounts and are not run live here; the integration
 * job in CI covers the databases it can start.
 */
import { test, expect, type Page } from '@playwright/test';
import { mkdtempSync, readFileSync, existsSync, rmSync } from 'fs';
import { tmpdir } from 'os';
import { join } from 'path';
import {
  api,
  Cleanup,
  createSink,
  createSource,
  createWorkflow,
  login,
  postWebhook,
  Receiver,
  stamp,
  startWorkflow,
  webhookPath,
} from './helpers';

const cleanup = new Cleanup();
test.afterEach(async () => cleanup.run());

async function receiver() {
  const r = new Receiver();
  const base = await r.start();
  cleanup.add(() => r.stop());
  return { r, base };
}

/** source -> [middle] -> sink, turned on. */
async function liveFlow(
  page: Page,
  source: Record<string, unknown>,
  sink: Record<string, unknown>,
  middle?: Record<string, unknown>,
) {
  const tag = stamp();
  const src = await createSource(page, cleanup, { name: `cov-live-src-${tag}`, ...source });
  const snk = await createSink(page, cleanup, { name: `cov-live-snk-${tag}`, ...sink });
  const nodes: Record<string, unknown>[] = [{ id: 's', type: 'source', ref_id: src.id, x: 0, y: 0 }];
  const edges: Record<string, unknown>[] = [];
  let last = 's';
  if (middle) {
    nodes.push({ id: 'm', x: 250, y: 0, ...middle });
    edges.push({ id: 'e0', source_id: 's', target_id: 'm' });
    last = 'm';
  }
  nodes.push({ id: 'k', type: 'sink', ref_id: snk.id, x: 500, y: 0 });
  edges.push({ id: 'e1', source_id: last, target_id: 'k' });
  const wf = await createWorkflow(page, cleanup, { name: `cov-live-wf-${tag}`, nodes, edges });
  await startWorkflow(page, wf.id);
  return { workflowId: wf.id, tag };
}

const httpSink = (base: string, path: string) => ({ type: 'http', config: { url: `${base}${path}`, method: 'POST' } });

test('L1: a form submission flows to an HTTP endpoint', async ({ page }) => {
  await login(page);
  const { r, base } = await receiver();
  const path = `/api/forms/cov-form-${stamp()}`;
  await liveFlow(page, { type: 'form', config: { path } }, httpSink(base, '/l1'));
  const res = await page.request.post(path, { data: { email: 'ada@example.com', plan: 'pro' }, headers: { Cookie: '' } });
  expect(res.status(), await res.text()).toBeLessThan(300);
  const got = await r.waitFor('/l1');
  expect(got).toHaveLength(1);
  expect(JSON.stringify(got[0].body)).toContain('ada@example.com');
});

test('L2: a cron schedule fires and its payload arrives', async ({ page }) => {
  await login(page);
  const { r, base } = await receiver();
  const marker = `tick-${stamp()}`;
  await liveFlow(
    page,
    { type: 'cron', config: { schedule: '@every 2s', payload: JSON.stringify({ marker }) } },
    httpSink(base, '/l2'),
  );
  const got = await r.waitFor('/l2', 1, 30000);
  expect(got.length, 'the schedule never fired').toBeGreaterThanOrEqual(1);
  expect(JSON.stringify(got[0].body)).toContain(marker);
});

test('L3: an HTTP polling source reads its URL and passes the data on', async ({ page }) => {
  await login(page);
  const { r, base } = await receiver();
  const marker = `polled-${stamp()}`;
  r.serve('/feed', { id: 9, marker });
  await liveFlow(
    page,
    { type: 'http', config: { url: `${base}/feed`, method: 'GET', poll_interval: '2s' } },
    httpSink(base, '/l3'),
  );
  const got = await r.waitFor('/l3', 1, 30000);
  expect(got.length, 'nothing was polled and delivered').toBeGreaterThanOrEqual(1);
  expect(JSON.stringify(got[0].body)).toContain(marker);
});

test('L4: a webhook record is written to a file', async ({ page }) => {
  await login(page);
  const dir = mkdtempSync(join(tmpdir(), 'hermod-e2e-'));
  cleanup.add(async () => rmSync(dir, { recursive: true, force: true }));
  const file = join(dir, 'events.jsonl');
  const hook = webhookPath(`cov-file-${stamp()}`);
  const marker = `filed-${stamp()}`;
  await liveFlow(page, { type: 'webhook', config: { path: hook } }, { type: 'file', config: { filename: file } });
  await postWebhook(page, hook, { marker });
  await expect
    .poll(() => (existsSync(file) ? readFileSync(file, 'utf8') : ''), { message: 'the file sink wrote nothing', timeout: 20000 })
    .toContain(marker);
});

test('L5: a webhook record becomes a row in a SQLite table', async ({ page }) => {
  await login(page);
  const dir = mkdtempSync(join(tmpdir(), 'hermod-e2e-'));
  cleanup.add(async () => rmSync(dir, { recursive: true, force: true }));
  const sinkConfig = { path: join(dir, 'out.db'), table: 'events' };
  const hook = webhookPath(`cov-sqlite-${stamp()}`);
  const marker = `row-${stamp()}`;
  await liveFlow(page, { type: 'webhook', config: { path: hook } }, { type: 'sqlite', config: sinkConfig });
  await postWebhook(page, hook, { id: 1, marker });
  await expect
    .poll(
      async () =>
        JSON.stringify(
          // The SQL console's endpoint, run against the sink's own database.
          await api(page, 'POST', '/api/sinks/query', {
            config: { type: 'sqlite', vhost: 'default', config: sinkConfig },
            query: 'SELECT * FROM events',
          }).catch((e) => String(e)),
        ),
      { message: 'the row never reached the table', timeout: 20000 },
    )
    .toContain(marker);
});

test('L6: a webhook record goes through to stdout', async ({ page }) => {
  await login(page);
  const hook = webhookPath(`cov-stdout-${stamp()}`);
  const marker = `printed-${stamp()}`;
  await liveFlow(page, { type: 'webhook', config: { path: hook } }, { type: 'stdout' });
  await postWebhook(page, hook, { marker });
  // Stdout is the worker's own output. scripts/dev.sh, which CI runs too,
  // sends it to .dev/logs/backend.log; a server started another way has no
  // file this test can read, and the check says so instead of passing.
  const log = join(process.cwd(), '.dev', 'logs', 'backend.log');
  test.skip(!existsSync(log), `no worker log at ${log}`);
  await expect
    .poll(() => readFileSync(log, 'utf8').includes(marker), { message: 'the record never reached stdout', timeout: 20000 })
    .toBe(true);
});

test('L7: a Filter whose conditions are stored as a list lets only matching records through', async ({ page }) => {
  await login(page);
  const { r, base } = await receiver();
  const hook = webhookPath(`cov-filter-${stamp()}`);
  await liveFlow(
    page,
    { type: 'webhook', config: { path: hook } },
    httpSink(base, '/l7'),
    {
      type: 'transformation',
      config: {
        transType: 'filter_data',
        // A list, as the API and an import store it -- the shape the engine
        // used to ignore, passing every record.
        conditions: [{ field: 'array_len(source.users)', operator: '>', value: '0' }],
      },
    },
  );
  await postWebhook(page, hook, { id: 1, users: [{ id: 1 }] });
  await postWebhook(page, hook, { id: 2, users: [] });
  await r.waitFor('/l7', 2, 8000); // give a wrongly passed second record time to arrive
  expect(r.received.filter((d) => d.path === '/l7').map((d) => d.body.id)).toEqual([1]);
});
