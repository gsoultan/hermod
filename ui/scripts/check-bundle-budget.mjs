#!/usr/bin/env node
/**
 * Fail the build if the critical path grows past its budget.
 *
 * "Critical path" here is every script and stylesheet index.html references —
 * what a browser must fetch before it can paint the login screen. It was
 * 1,092,246 bytes with hand-rolled vendor buckets, and dropped to ~754,000 once
 * those were removed, because bucketing had been promoting the workflow
 * editor's graph library onto the login route. That regression is one
 * well-meaning `manualChunks` entry or one eager import away from coming back,
 * and nothing else in CI would notice.
 *
 * The budget is deliberately close to the measured figure. A vendor bucket
 * regression adds hundreds of kilobytes; ordinary feature churn adds a few.
 *
 *   node scripts/check-bundle-budget.mjs            # check against the budget
 *   node scripts/check-bundle-budget.mjs --report   # print, never fail
 */
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const dist = path.resolve(here, '..', 'dist');

/** Bytes, uncompressed. Raw is what the parser has to process; compressed size
 *  varies with the CDN and is reported but not gated. */
const BUDGET_RAW = 800_000;

const report = process.argv.includes('--report');

const indexPath = path.join(dist, 'index.html');
if (!fs.existsSync(indexPath)) {
  console.error(`bundle budget: ${indexPath} not found — run \`bun run build\` first`);
  process.exit(2);
}

const html = fs.readFileSync(indexPath, 'utf8');
const refs = [...html.matchAll(/(?:src|href)="\/(assets\/[^"]+\.(?:js|css))"/g)].map((m) => m[1]);
const unique = [...new Set(refs)];

if (unique.length === 0) {
  console.error('bundle budget: index.html references no assets — the build output looks wrong');
  process.exit(2);
}

let raw = 0;
let br = 0;
const rows = [];
for (const rel of unique) {
  const file = path.join(dist, rel);
  const size = fs.statSync(file).size;
  const brFile = `${file}.br`;
  const brSize = fs.existsSync(brFile) ? fs.statSync(brFile).size : 0;
  raw += size;
  br += brSize;
  rows.push({ rel, size, brSize });
}

rows.sort((a, b) => b.size - a.size);
const kb = (n) => `${(n / 1024).toFixed(1)} kB`;

console.log(`critical path: ${unique.length} files, ${kb(raw)} raw, ${kb(br)} brotli (budget ${kb(BUDGET_RAW)} raw)`);
for (const r of rows.slice(0, 8)) {
  console.log(`  ${kb(r.size).padStart(10)}  ${r.rel}`);
}

if (raw > BUDGET_RAW && !report) {
  console.error(
    `\nbundle budget exceeded by ${kb(raw - BUDGET_RAW)}.\n` +
      'Something now on the login route did not use to be. Check for a new eager\n' +
      'import of a page-only library, or a manualChunks entry in vite.config.ts —\n' +
      'see the comment there for why grouping is not free.'
  );
  process.exit(1);
}
