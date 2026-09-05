#!/usr/bin/env node
/**
 * Where does the editor allocate under load?
 *
 * measure-editor.mjs answers "how much"; this answers "from which functions".
 * Same fixture and traffic, but with V8's sampling heap profiler running for
 * the window, then a top-N of self-allocated bytes by function. Use it when a
 * heap number moves and the reason is not obvious.
 *
 *   node scripts/profile-editor-allocations.mjs http://localhost:5175 30
 */
import { chromium } from 'playwright';

const base = process.argv[2] || 'http://localhost:5175';
const SECONDS = Number(process.argv[3] || 30);
const MSGS_PER_SEC = 10;
const TOP = 20;

const browser = await chromium.launch();
const context = await browser.newContext({ viewport: { width: 1600, height: 1000 }, baseURL: base });
const page = await context.newPage();

await page.goto('/login');
await page.getByPlaceholder('Your username').fill(process.env.HERMOD_E2E_USER || 'admin');
await page.getByPlaceholder('Your password').fill(process.env.HERMOD_E2E_PASS || 'admin');
await page.click('button:has-text("Sign In")');
await page.waitForURL((u) => !u.pathname.includes('/login'), { timeout: 30000 });

const api = (path, init) =>
  page.evaluate(
    async ([p, method, body]) => {
      const headers = { 'Content-Type': 'application/json' };
      const m = document.cookie.match(/(?:^|;\s*)hermod_csrf=([^;]*)/);
      if (m) headers['X-CSRF-Token'] = decodeURIComponent(m[1]);
      const res = await fetch(p, { method, credentials: 'include', headers, body: body || undefined });
      return { status: res.status, body: await res.text() };
    },
    [path, init?.method || 'GET', init?.body ? JSON.stringify(init.body) : '']
  );

const name = `profile-${Date.now()}`;
const hook = `/api/webhooks/${name}`;
const src = await api('/api/sources', { method: 'POST', body: { name: `${name}-src`, type: 'webhook', config: { path: hook }, vhost: 'default' } });
const snk = await api('/api/sinks', { method: 'POST', body: { name: `${name}-snk`, type: 'stdout', config: {}, vhost: 'default' } });
const srcID = JSON.parse(src.body).id;
const snkID = JSON.parse(snk.body).id;
const nodes = [{ id: 'n-src', type: 'source', ref_id: srcID, x: 60, y: 300 }];
const edges = [];
let prev = 'n-src';
for (let i = 0; i < 12; i++) {
  const id = `n-tx-${i}`;
  nodes.push({ id, type: 'transformation', config: { transType: 'set', [`column.f${i}`]: `'v${i}'` }, x: 260 + i * 200, y: 300 });
  edges.push({ id: `e-${i}`, source_id: prev, target_id: id });
  prev = id;
}
nodes.push({ id: 'n-snk', type: 'sink', ref_id: snkID, x: 260 + 12 * 200, y: 300 });
edges.push({ id: 'e-snk', source_id: prev, target_id: 'n-snk' });
const wf = await api('/api/workflows', { method: 'POST', body: { name, vhost: 'default', active: false, nodes, edges } });
const wfID = JSON.parse(wf.body).id;

await api(`/api/workflows/${wfID}/toggle`, { method: 'POST', body: {} });
let up = false;
for (let i = 0; i < 20 && !up; i++) {
  await new Promise((r) => setTimeout(r, 500));
  up = (await fetch(`${base}${hook}`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{"probe":true}' })).ok;
}
if (!up) console.warn('source did not come up; profiling an idle editor');

await page.goto(`/workflows/${wfID}/edit`);
await page.locator('.react-flow__node').first().waitFor({ timeout: 30000 });
await page.waitForTimeout(2000);

const cdp = await context.newCDPSession(page);
await cdp.send('HeapProfiler.enable');
await cdp.send('HeapProfiler.collectGarbage').catch(() => {});
// 32 KiB sampling: fine-grained enough to attribute a few MB, cheap enough not
// to perturb the thing being measured.
await cdp.send('HeapProfiler.startSampling', { samplingInterval: 32768 });

let sent = 0;
const pump = setInterval(() => {
  sent++;
  fetch(`${base}${hook}`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ n: sent, ts: Date.now(), payload: 'x'.repeat(200) }) }).catch(() => {});
}, 1000 / MSGS_PER_SEC);
await page.waitForTimeout(SECONDS * 1000);
clearInterval(pump);

const { profile } = await cdp.send('HeapProfiler.stopSampling');

// Sum self-size per function, then per file, walking the call tree.
const byFn = new Map();
const byFile = new Map();
const walk = (node) => {
  const { callFrame, selfSize } = node;
  if (selfSize > 0) {
    const file = (callFrame.url || '(anon)').split('/').slice(-2).join('/').split('?')[0];
    const fn = `${callFrame.functionName || '(anonymous)'}  ${file}:${callFrame.lineNumber + 1}`;
    byFn.set(fn, (byFn.get(fn) || 0) + selfSize);
    byFile.set(file, (byFile.get(file) || 0) + selfSize);
  }
  for (const c of node.children || []) walk(c);
};
walk(profile.head);

const total = [...byFn.values()].reduce((a, b) => a + b, 0);
const mb = (b) => (b / 1048576).toFixed(2);
const top = (m) => [...m.entries()].sort((a, b) => b[1] - a[1]).slice(0, TOP);

console.log(`\n== sampled allocations over ${SECONDS}s, ${sent} messages — ${base}`);
console.log(`   total sampled: ${mb(total)} MB\n`);
console.log('-- by file --');
for (const [f, b] of top(byFile)) console.log(`  ${mb(b).padStart(8)} MB  ${(100 * b / total).toFixed(1).padStart(5)}%  ${f}`);
console.log('\n-- by function --');
for (const [f, b] of top(byFn)) console.log(`  ${mb(b).padStart(8)} MB  ${f}`);

await api(`/api/workflows/${wfID}/toggle`, { method: 'POST', body: {} }).catch(() => {});
await api(`/api/workflows/${wfID}`, { method: 'DELETE' }).catch(() => {});
await api(`/api/sources/${srcID}`, { method: 'DELETE' }).catch(() => {});
await api(`/api/sinks/${snkID}`, { method: 'DELETE' }).catch(() => {});
await browser.close();
