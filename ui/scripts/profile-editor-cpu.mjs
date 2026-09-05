#!/usr/bin/env node
/**
 * Where does the editor spend main-thread time under load?
 *
 * The question behind "should more work move to a worker" is whether the main
 * thread is actually blocked, and by what. This drives the same 14-node
 * workflow with live webhook traffic as measure-editor.mjs, records a CPU
 * profile for the window, and reports (a) long tasks — >50 ms main-thread
 * blocks, the thing users feel — and (b) self-time by function and by file.
 *
 *   node scripts/profile-editor-cpu.mjs http://localhost:5175 30
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

const api = (p, init) =>
  page.evaluate(
    async ([p, method, body]) => {
      const headers = { 'Content-Type': 'application/json' };
      const m = document.cookie.match(/(?:^|;\s*)hermod_csrf=([^;]*)/);
      if (m) headers['X-CSRF-Token'] = decodeURIComponent(m[1]);
      const res = await fetch(p, { method, credentials: 'include', headers, body: body || undefined });
      return { status: res.status, body: await res.text() };
    },
    [p, init?.method || 'GET', init?.body ? JSON.stringify(init.body) : '']
  );

const name = `cpu-${Date.now()}`;
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

const toggled = await api(`/api/workflows/${wfID}/toggle`, { method: 'POST', body: {} });
let up = false;
for (let i = 0; i < 20 && !up; i++) {
  await new Promise((r) => setTimeout(r, 500));
  up = (await fetch(`${base}${hook}`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{"probe":true}' })).ok;
}
if (!up) console.warn(`source did not come up (toggle ${toggled.status}: ${toggled.body.slice(0, 160)}); profiling an idle editor`);

await page.goto(`/workflows/${wfID}/edit`);
await page.locator('.react-flow__node').first().waitFor({ timeout: 30000 });
await page.waitForTimeout(2000);

// Long tasks are observed in-page; the CPU profile comes from CDP.
await page.evaluate(() => {
  window.__longTasks = [];
  new PerformanceObserver((list) => {
    for (const e of list.getEntries()) window.__longTasks.push({ start: Math.round(e.startTime), ms: Math.round(e.duration) });
  }).observe({ type: 'longtask', buffered: true });
});

const cdp = await context.newCDPSession(page);
await cdp.send('Profiler.enable');
await cdp.send('Profiler.setSamplingInterval', { interval: 500 }); // 0.5 ms
await cdp.send('Profiler.start');

let sent = 0;
const pump = setInterval(() => {
  sent++;
  fetch(`${base}${hook}`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ n: sent, ts: Date.now(), payload: 'x'.repeat(200) }) }).catch(() => {});
}, 1000 / MSGS_PER_SEC);
await page.waitForTimeout(SECONDS * 1000);
clearInterval(pump);

const { profile } = await cdp.send('Profiler.stop');
const longTasks = await page.evaluate(() => window.__longTasks);

// --- self time by function / file ---
const byId = new Map(profile.nodes.map((n) => [n.id, n]));
const selfUs = new Map();
const dt = profile.timeDeltas;
for (let i = 0; i < profile.samples.length; i++) {
  const id = profile.samples[i];
  selfUs.set(id, (selfUs.get(id) || 0) + (dt[i] || 0));
}
const byFn = new Map();
const byFile = new Map();
let totalUs = 0;
let idleUs = 0;
for (const [id, us] of selfUs) {
  const n = byId.get(id);
  const cf = n.callFrame;
  const fnName = cf.functionName || '(anonymous)';
  if (fnName === '(idle)' || fnName === '(program)' || fnName === '(garbage collector)') {
    if (fnName === '(idle)') idleUs += us;
    totalUs += us;
    byFn.set(fnName, (byFn.get(fnName) || 0) + us);
    continue;
  }
  totalUs += us;
  const file = (cf.url || '(native)').split('/').slice(-2).join('/').split('?')[0];
  byFn.set(`${fnName}  ${file}:${cf.lineNumber + 1}`, (byFn.get(`${fnName}  ${file}:${cf.lineNumber + 1}`) || 0) + us);
  byFile.set(file, (byFile.get(file) || 0) + us);
}

const ms = (us) => (us / 1000).toFixed(0).padStart(6);
const pct = (us) => ((100 * us) / totalUs).toFixed(1).padStart(5);
const top = (m) => [...m.entries()].sort((a, b) => b[1] - a[1]).slice(0, TOP);
const busyUs = totalUs - idleUs;

console.log(`\n== editor main thread over ${SECONDS}s, ${sent} messages — ${base}`);
console.log(`   busy ${ms(busyUs)} ms of ${ms(totalUs)} ms (${pct(busyUs)}% ), idle ${pct(idleUs)}%`);
console.log(`   long tasks (>50ms): ${longTasks.length}` + (longTasks.length ? `  worst ${Math.max(...longTasks.map((t) => t.ms))} ms  total ${longTasks.reduce((a, t) => a + t.ms, 0)} ms` : ''));
if (longTasks.length) console.log('   ' + longTasks.slice(0, 12).map((t) => `${t.ms}ms@${(t.start / 1000).toFixed(1)}s`).join('  '));
console.log('\n-- self time by file (excluding idle) --');
for (const [f, us] of top(byFile)) console.log(`  ${ms(us)} ms  ${pct(us)}%  ${f}`);
console.log('\n-- self time by function --');
for (const [f, us] of top(byFn)) console.log(`  ${ms(us)} ms  ${pct(us)}%  ${f}`);

await api(`/api/workflows/${wfID}/toggle`, { method: 'POST', body: {} }).catch(() => {});
await api(`/api/workflows/${wfID}`, { method: 'DELETE' }).catch(() => {});
await api(`/api/sources/${srcID}`, { method: 'DELETE' }).catch(() => {});
await api(`/api/sinks/${snkID}`, { method: 'DELETE' }).catch(() => {});
await browser.close();
