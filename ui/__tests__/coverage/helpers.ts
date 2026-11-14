/**
 * Shared plumbing for the coverage suite: the API, an error watch every test
 * runs under, and the fixtures parameterised pages need.
 */
import { expect, type Page, type Response } from '@playwright/test';
import { apiRequest } from '../support/auth';
import { createServer, type Server } from 'http';
import type { AddressInfo } from 'net';

export { login } from '../support/auth';

/** A unique, sortable suffix for the names a test creates. */
export const stamp = () => `${Date.now()}-${Math.floor(Math.random() * 1e6)}`;

/** Call the API as the logged-in user; throw with the body on any 4xx/5xx. */
export async function api<T = any>(page: Page, method: string, path: string, body?: unknown): Promise<T> {
  const res = await apiRequest(page, path, { method, body });
  if (res.status >= 400) {
    throw new Error(`${method} ${path} answered ${res.status}: ${res.body.slice(0, 500)}`);
  }
  return (res.body ? JSON.parse(res.body) : null) as T;
}

/** The same, but hand back the status instead of throwing. */
export async function apiRaw(page: Page, method: string, path: string, body?: unknown) {
  return apiRequest(page, path, { method, body });
}

// ---------------------------------------------------------------------------
// Error watch
// ---------------------------------------------------------------------------

/**
 * Console noise that is not a defect of the page under test. Each entry says
 * why; anything else logged at error level fails the test.
 */
const BENIGN_CONSOLE: RegExp[] = [
  // The browser's own line for a request the test expected to fail (a 4xx from
  // a deliberately bad Test Connection, say). The response itself is checked
  // separately, by status.
  /Failed to load resource: the server responded with a status of 4\d\d/,
  // A live stream torn down because the test navigated away mid-handshake.
  /WebSocket connection to .* failed/i,
  /WebSocket is closed before the connection is established/i,
  // Sockets and streams the test's own navigation aborts.
  /net::ERR_ABORTED/,
];

export interface ErrorWatch {
  /** Every problem seen since the last reset, formatted for a failure message. */
  problems(): string[];
  reset(): void;
  /** Fail the test, naming `where`, if anything went wrong. */
  expectClean(where: string): void;
}

/**
 * Collects what a broken page leaves behind: an uncaught exception, a console
 * error, a 5xx from the API, and the router's error page. A test that clicks
 * through a page and checks nothing else still fails when any of these appear.
 *
 * `allow5xx` lists paths whose server errors are the point of the test.
 */
export function watchErrors(page: Page, opts: { allow5xx?: RegExp[] } = {}): ErrorWatch {
  let seen: string[] = [];
  page.on('pageerror', (err) => seen.push(`uncaught exception: ${err.message}`));
  page.on('console', (msg) => {
    if (msg.type() !== 'error') return;
    const text = msg.text();
    if (BENIGN_CONSOLE.some((re) => re.test(text))) return;
    seen.push(`console error: ${text.slice(0, 400)}`);
  });
  page.on('response', (res: Response) => {
    const url = res.url();
    if (res.status() < 500 || !url.includes('/api/')) return;
    if (opts.allow5xx?.some((re) => re.test(url))) return;
    seen.push(`server error ${res.status()} from ${res.request().method()} ${new URL(url).pathname}`);
  });
  return {
    problems: () => [...seen],
    reset: () => {
      seen = [];
    },
    expectClean(where: string) {
      const found = [...seen];
      seen = [];
      expect(found, `${where}: the page reported problems`).toEqual([]);
    },
  };
}

/** The router's error page is up (ErrorPage.tsx shows "Technical Details"). */
export async function expectNoErrorPage(page: Page, where: string) {
  await expect(page.getByText('Technical Details', { exact: true }), `${where} rendered the error page`).toHaveCount(0);
}

/** Wait for the page to stop fetching, without failing on long-lived streams. */
export async function settle(page: Page, ms = 400) {
  await page.waitForLoadState('domcontentloaded');
  await page.waitForLoadState('networkidle', { timeout: 5000 }).catch(() => {});
  await page.waitForTimeout(ms);
}

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

export interface Fixtures {
  sourceId: string;
  sinkId: string;
  workflowId: string;
  vhostId: string;
  vhostName: string;
  userId: string;
  workerId?: string;
}

/** Everything a test created, removed again in reverse order. */
export class Cleanup {
  private steps: Array<() => Promise<unknown>> = [];
  add(step: () => Promise<unknown>) {
    this.steps.push(step);
  }
  async run() {
    for (const step of this.steps.reverse()) {
      await step().catch(() => {});
    }
    this.steps = [];
  }
}

export async function createSource(page: Page, cleanup: Cleanup, body: Record<string, unknown>) {
  const s = await api(page, 'POST', '/api/sources', { vhost: 'default', active: true, config: {}, ...body });
  cleanup.add(() => apiRaw(page, 'DELETE', `/api/sources/${s.id}`));
  return s as { id: string; name: string };
}

export async function createSink(page: Page, cleanup: Cleanup, body: Record<string, unknown>) {
  const s = await api(page, 'POST', '/api/sinks', { vhost: 'default', config: {}, ...body });
  cleanup.add(() => apiRaw(page, 'DELETE', `/api/sinks/${s.id}`));
  return s as { id: string; name: string };
}

export async function createWorkflow(page: Page, cleanup: Cleanup, body: Record<string, unknown>) {
  const w = await api(page, 'POST', '/api/workflows', { vhost: 'default', active: false, ...body });
  cleanup.add(async () => {
    // A running workflow refuses deletion; stop it first.
    const cur = await apiRaw(page, 'GET', `/api/workflows/${w.id}`);
    if (cur.status === 200 && JSON.parse(cur.body).active) {
      await apiRaw(page, 'POST', `/api/workflows/${w.id}/toggle`);
    }
    return apiRaw(page, 'DELETE', `/api/workflows/${w.id}`);
  });
  return w as { id: string; name: string };
}

/**
 * A webhook source, a stdout sink and a one-step workflow between them: the
 * smallest set that gives every `$id` route something real to open.
 */
export async function createBasicFixtures(page: Page, cleanup: Cleanup): Promise<Fixtures> {
  const tag = stamp();
  const source = await createSource(page, cleanup, {
    name: `cov-src-${tag}`,
    type: 'webhook',
    config: { path: webhookPath(`cov-${tag}`) },
  });
  await api(page, 'PUT', `/api/sources/${source.id}/sample`, { sample: JSON.stringify({ id: 1, name: 'Ada' }) });
  const sink = await createSink(page, cleanup, { name: `cov-snk-${tag}`, type: 'stdout' });
  const workflow = await createWorkflow(page, cleanup, {
    name: `cov-wf-${tag}`,
    nodes: [
      { id: 'n-src', type: 'source', ref_id: source.id, x: 40, y: 120 },
      { id: 'n-set', type: 'transformation', x: 320, y: 120, config: { transType: 'set', 'column.tagged': "'yes'" } },
      { id: 'n-snk', type: 'sink', ref_id: sink.id, x: 600, y: 120 },
    ],
    edges: [
      { id: 'e1', source_id: 'n-src', target_id: 'n-set' },
      { id: 'e2', source_id: 'n-set', target_id: 'n-snk' },
    ],
  });
  const vhostName = `cov-vh-${tag}`;
  const vhost = await api(page, 'POST', '/api/vhosts', { name: vhostName, description: 'coverage suite' });
  cleanup.add(() => apiRaw(page, 'DELETE', `/api/vhosts/${vhost.id}`));
  const user = await api(page, 'POST', '/api/users', {
    username: `cov-u-${tag}`.slice(0, 30),
    password: 'Coverage-Passw0rd!',
    full_name: 'Coverage User',
    email: `cov-${tag}@example.com`,
    role: 'Viewer',
    vhosts: ['default'],
  });
  cleanup.add(() => apiRaw(page, 'DELETE', `/api/users/${user.id}`));
  const workers = await apiRaw(page, 'GET', '/api/workers');
  let workerId: string | undefined;
  if (workers.status === 200) {
    const list = JSON.parse(workers.body);
    workerId = (Array.isArray(list) ? list : list.data ?? [])[0]?.id;
  }
  return {
    sourceId: source.id,
    sinkId: sink.id,
    workflowId: workflow.id,
    vhostId: vhost.id,
    vhostName,
    userId: user.id,
    workerId,
  };
}

// ---------------------------------------------------------------------------
// A local HTTP endpoint for sinks to deliver to
// ---------------------------------------------------------------------------


export interface Delivery {
  path: string;
  body: any;
}

/**
 * An HTTP server on 127.0.0.1 that records what an HTTP sink sends it. Hermod
 * runs on the same machine in CI and locally, so a workflow can deliver here
 * and the test can read what arrived -- live delivery with no outside service.
 *
 * It answers 200 to every method, because the sink pings its URL before a
 * workflow starts and refuses to start on anything else.
 */
export class Receiver {
  private server?: Server;
  readonly received: Delivery[] = [];
  /** Bodies to answer a GET with, by path -- what an HTTP polling source reads. */
  private readonly served = new Map<string, unknown>();

  /** Answer GET `path` with `body` as JSON. */
  serve(path: string, body: unknown) {
    this.served.set(path, body);
  }

  async start(): Promise<string> {
    this.server = createServer((req, res) => {
      const chunks: Buffer[] = [];
      req.on('data', (c) => chunks.push(c));
      req.on('end', () => {
        if (req.method === 'POST' || req.method === 'PUT') {
          const text = Buffer.concat(chunks).toString('utf8');
          let body: any = text;
          try {
            body = JSON.parse(text);
          } catch {}
          this.received.push({ path: req.url || '/', body });
        }
        const path = (req.url || '/').split('?')[0];
        res.writeHead(200, { 'Content-Type': 'application/json' });
        res.end(req.method === 'GET' && this.served.has(path) ? JSON.stringify(this.served.get(path)) : '{"ok":true}');
      });
    });
    await new Promise<void>((resolve) => this.server!.listen(0, '127.0.0.1', resolve));
    return `http://127.0.0.1:${(this.server!.address() as AddressInfo).port}`;
  }

  /** What arrived at `path`, waiting up to `timeout` for at least `count`. */
  async waitFor(path: string, count = 1, timeout = 20000): Promise<Delivery[]> {
    const deadline = Date.now() + timeout;
    for (;;) {
      const got = this.received.filter((d) => d.path === path);
      if (got.length >= count || Date.now() > deadline) return got;
      await new Promise((r) => setTimeout(r, 200));
    }
  }

  async stop() {
    await new Promise<void>((resolve) => (this.server ? this.server.close(() => resolve()) : resolve()));
  }
}

/** Turn a workflow on and wait until the engine reports it running. */
export async function startWorkflow(page: Page, id: string) {
  await api(page, 'POST', `/api/workflows/${id}/toggle`);
  await expect
    .poll(async () => (await api(page, 'GET', `/api/workflows/${id}`)).status, {
      message: 'the workflow did not start',
      timeout: 30000,
    })
    .toMatch(/active|running/i);
}

/** The path a webhook source named `name` listens on. */
export const webhookPath = (name: string) => `/api/webhooks/${name}`;

/** Post to a webhook source the way an outside caller would: no session. */
export async function postWebhook(page: Page, path: string, body: unknown) {
  const res = await page.request.post(path, { data: body, headers: { Cookie: '' } });
  expect(res.status(), `webhook ${path} refused: ${await res.text()}`).toBeLessThan(300);
}
