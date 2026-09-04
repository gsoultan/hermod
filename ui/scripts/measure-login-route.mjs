#!/usr/bin/env node
/**
 * Paint and stylesheet cost of the login route, against a served build.
 *
 * The editor benchmark (measure-editor.mjs) runs against the dev server, which
 * serves CSS unbundled; that is fine for heap but says nothing about what
 * production ships. Point this at `vite preview` for the real numbers:
 *
 *   bun run build && bunx vite preview --port 4173 &
 *   node scripts/measure-login-route.mjs http://localhost:4173
 *
 * Reports the median of several cold loads so one slow disk read does not
 * become the headline.
 */
import { chromium } from 'playwright';

const base = process.argv[2] || 'http://localhost:4173';
const RUNS = Number(process.argv[3] || 5);

const browser = await chromium.launch();
const runs = [];

for (let i = 0; i < RUNS; i++) {
  // Fresh context per run: no cache, no service worker from the last one.
  const ctx = await browser.newContext();
  const page = await ctx.newPage();
  await page.goto(`${base}/login`, { waitUntil: 'load' });
  await page.waitForTimeout(400);
  runs.push(
    await page.evaluate(() => {
      const res = performance.getEntriesByType('resource');
      const css = res.filter((r) => r.name.endsWith('.css'));
      const js = res.filter((r) => r.name.endsWith('.js'));
      const paint = Object.fromEntries(performance.getEntriesByType('paint').map((p) => [p.name, p.startTime]));
      const nav = performance.getEntriesByType('navigation')[0];
      const sum = (xs, k) => xs.reduce((a, r) => a + (r[k] || 0), 0);
      return {
        cssDecoded: sum(css, 'decodedBodySize'),
        cssTransfer: sum(css, 'transferSize'),
        cssMaxMs: Math.max(0, ...css.map((r) => r.responseEnd)),
        jsDecoded: sum(js, 'decodedBodySize'),
        fcp: paint['first-contentful-paint'] ?? null,
        dcl: nav?.domContentLoadedEventEnd ?? null,
      };
    })
  );
  await ctx.close();
}
await browser.close();

const median = (k) => {
  const xs = runs.map((r) => r[k]).filter((x) => x != null).sort((a, b) => a - b);
  return xs.length ? xs[Math.floor(xs.length / 2)] : null;
};
const kb = (n) => `${(n / 1024).toFixed(1)} kB`;
const ms = (n) => (n == null ? 'n/a' : `${Math.round(n)} ms`);

console.log(`== login route, median of ${RUNS} cold loads — ${base}`);
console.log(`  css   ${kb(median('cssDecoded'))} decoded, ${kb(median('cssTransfer'))} over the wire, last byte at ${ms(median('cssMaxMs'))}`);
console.log(`  js    ${kb(median('jsDecoded'))} decoded`);
console.log(`  first-contentful-paint ${ms(median('fcp'))}   DOMContentLoaded ${ms(median('dcl'))}`);
