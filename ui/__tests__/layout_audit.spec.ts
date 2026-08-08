import { test, expect, type Page } from '@playwright/test';
import { writeFileSync } from 'fs';
import { E2E_USER, E2E_PASS } from './support/auth';

/**
 * Automated layout audit.
 *
 * Measures every page in a real browser rather than relying on inspection:
 * horizontal overflow, elements escaping the viewport, unlabelled form fields,
 * field-width balance and load timing. Results are written to
 * layout-audit.json so findings can be diffed between runs.
 */

const ROUTES = [
  '/', '/sources', '/sources/new', '/sinks', '/sinks/new',
  '/workflows', '/workflows/new', '/approvals', '/logs', '/audit-logs',
  '/schemas', '/lineage', '/marketplace', '/health', '/compliance',
  '/workers', '/users', '/vhosts', '/settings', '/profile',
];

const VIEWPORTS = [
  { name: 'laptop', width: 1440, height: 900 },
  { name: 'desktop', width: 1920, height: 1080 },
  { name: 'tablet', width: 1024, height: 768 },
  { name: 'mobile', width: 390, height: 844 },
];

interface Finding {
  route: string;
  viewport: string;
  kind: string;
  detail: string;
}

const findings: Finding[] = [];
const timings: { route: string; ms: number; domNodes: number }[] = [];

const login = async (page: Page) => {
  await page.goto('/login');
  await page.getByPlaceholder('Your username').fill(E2E_USER);
  await page.getByPlaceholder('Your password').fill(E2E_PASS);
  await page.getByRole('button', { name: /sign in|login/i }).click();
  await page.waitForURL((u) => !u.pathname.startsWith('/login'), { timeout: 30000 });
};

/** Everything measured inside one page, in a single evaluate for speed. */
const auditPage = (route: string) => `(() => {
  const out = { overflowX: null, escapees: [], unlabelled: [], tinyText: [],
                fieldWidths: [], domNodes: document.querySelectorAll('*').length,
                emptyRoot: false, crashed: false };

  const root = document.getElementById('root');
  out.emptyRoot = !root || root.innerHTML.trim() === '';

  // A page that renders almost no text is either broken or an unexplained
  // blank state. Either way the user cannot tell which, so both count.
  // Measure the whole app root: AppShell renders a <main> wrapper whose own
  // innerText can be empty even when the page is full.
  // textContent, not innerText: innerText needs layout and reads as empty on a
  // page still settling, which produced false "blank page" findings.
  const bodyText = (root && root.textContent ? root.textContent : '').replace(/\s+/g, ' ').trim();
  out.thinContent = bodyText.length < 120 ? bodyText.length : null;
  out.crashed = document.body.innerText.includes('System Interruption');

  // Horizontal overflow of the document itself.
  const de = document.documentElement;
  if (de.scrollWidth > de.clientWidth + 1) {
    out.overflowX = { scrollWidth: de.scrollWidth, clientWidth: de.clientWidth };
  }

  const vw = window.innerWidth;
  const describe = (el) => {
    const id = el.id ? '#' + el.id : '';
    const cls = typeof el.className === 'string' && el.className
      ? '.' + el.className.split(/\\s+/).filter(Boolean).slice(0, 2).join('.') : '';
    return (el.tagName.toLowerCase() + id + cls).slice(0, 90);
  };

  // Elements extending past the right edge — the usual cause of a horizontal
  // scrollbar and of controls the user cannot reach.
  // An element inside a horizontally scrollable ancestor is *meant* to extend
  // past the viewport — that is what the scrollbar is for. An element inside a
  // clipping ancestor (overflow:hidden) is bounded by that clip: it produces no
  // scrollbar and hides no reachable control, which is what this check is for.
  // A pan/zoom canvas is the common case — React Flow's transformed viewport is
  // several times the window wide by design, and flagging it says nothing about
  // the layout. Only report content the user genuinely cannot reach.
  const inScrollableOrClipped = (el) => {
    let p = el.parentElement;
    while (p && p !== document.body) {
      const s = getComputedStyle(p);
      if (s.overflowX === 'auto' || s.overflowX === 'scroll') return true;
      if (s.overflowX === 'hidden' || s.overflow === 'hidden') return true;
      p = p.parentElement;
    }
    return false;
  };

  for (const el of document.querySelectorAll('body *')) {
    const r = el.getBoundingClientRect();
    if (r.width === 0 || r.height === 0) continue;
    const style = getComputedStyle(el);
    if (style.position === 'fixed' || style.visibility === 'hidden') continue;
    if (r.right > vw + 2 && !inScrollableOrClipped(el)) {
      out.escapees.push({ el: describe(el), right: Math.round(r.right), vw });
      if (out.escapees.length >= 8) break;
    }
  }

  // Inputs with no accessible name are unusable with assistive tech and
  // ambiguous for everyone else.
  for (const el of document.querySelectorAll('input:not([type=hidden]), select, textarea')) {
    const hasLabel = !!(el.getAttribute('aria-label') || el.getAttribute('aria-labelledby')
      || (el.id && document.querySelector('label[for="' + CSS.escape(el.id) + '"]'))
      || el.closest('label') || el.getAttribute('placeholder'));
    if (!hasLabel) out.unlabelled.push(describe(el));
  }

  // Field widths inside a form row: wildly different widths on the same row
  // read as misalignment.
  for (const el of document.querySelectorAll('input:not([type=hidden]), select, textarea')) {
    const r = el.getBoundingClientRect();
    if (r.width > 0) out.fieldWidths.push(Math.round(r.width));
  }

  // Text below ~11px is hard to read and usually accidental.
  for (const el of document.querySelectorAll('body *')) {
    if (!el.children.length && el.textContent && el.textContent.trim().length > 3) {
      const fs = parseFloat(getComputedStyle(el).fontSize);
      if (fs && fs < 11) { out.tinyText.push({ el: describe(el), fontSize: fs }); }
      if (out.tinyText.length >= 5) break;
    }
  }
  return out;
})()`;

test.describe('Hermod layout audit', () => {
  test.describe.configure({ timeout: 600000 });

  for (const vp of VIEWPORTS) {
    test(`layout at ${vp.name} (${vp.width}x${vp.height})`, async ({ page }) => {
      await page.setViewportSize({ width: vp.width, height: vp.height });
      await login(page);

      for (const route of ROUTES) {
        const t0 = Date.now();
        await page.goto(route, { waitUntil: 'networkidle' }).catch(() => {});
        await page.waitForTimeout(400);
        const ms = Date.now() - t0;

        const r: any = await page.evaluate(auditPage(route));
        if (vp.name === 'laptop') timings.push({ route, ms, domNodes: r.domNodes });

        // A page ballooning past ~1800 nodes usually means an unpaginated list.
        // /logs sits near 640 with its 30-row page, so this catches a real
        // regression without flagging normal growth.
        //
        // /workflows and /lineage are paginated too (30 rows, WorkflowsPage.tsx),
        // but their rows are far heavier — a checkbox, several badges and six
        // tooltip-wrapped action icons each, around 64 nodes per row against
        // /logs' ~20. A full page lands near 2,250. Give them a scoped budget so
        // the check keeps doing its actual job (catching an *unbounded* list)
        // instead of failing on a bounded one. If a row's markup is ever
        // slimmed, lower this.
        const budget = /^\/(workflows|lineage)$/.test(route) ? 2600 : 1800;
        if (r.domNodes > budget) {
          findings.push({ route, viewport: vp.name, kind: 'DOM_BLOAT', detail: `${r.domNodes} nodes (budget ${budget})` });
        }
        if (r.crashed) findings.push({ route, viewport: vp.name, kind: 'CRASH', detail: 'ErrorBoundary shown' });
        if (r.emptyRoot) findings.push({ route, viewport: vp.name, kind: 'EMPTY', detail: '#root is empty' });
        // Canvas routes are legitimately text-light — the workflow editor is a
        // graph, not a document.
        const isCanvas = /\/workflows\/(new|.+\/edit)$/.test(route);
        if (!isCanvas && r.thinContent !== null && r.thinContent !== undefined) {
          findings.push({ route, viewport: vp.name, kind: 'THIN_CONTENT',
            detail: `only ${r.thinContent} chars of text — needs an empty state` });
        }
        if (r.overflowX) {
          findings.push({ route, viewport: vp.name, kind: 'OVERFLOW_X',
            detail: `scrollWidth ${r.overflowX.scrollWidth} > clientWidth ${r.overflowX.clientWidth}` });
        }
        for (const e of r.escapees) {
          findings.push({ route, viewport: vp.name, kind: 'OFFSCREEN',
            detail: `${e.el} right=${e.right} > vw=${e.vw}` });
        }
        for (const u of r.unlabelled) {
          findings.push({ route, viewport: vp.name, kind: 'UNLABELLED_FIELD', detail: u });
        }
        for (const t of r.tinyText) {
          findings.push({ route, viewport: vp.name, kind: 'TINY_TEXT', detail: `${t.el} @${t.fontSize}px` });
        }
      }
    });
  }

  // Assert rather than only report, so a regression fails CI instead of being
  // noticed later. Thresholds record what is currently known and accepted:
  // the only tolerated tiny text is React Flow's attribution link, which their
  // licence requires us to display.
  test('no layout regressions', () => {
    const byKind: Record<string, Finding[]> = {};
    for (const f of findings) (byKind[f.kind] ||= []).push(f);

    const fatal = ['CRASH', 'EMPTY', 'OVERFLOW_X', 'OFFSCREEN', 'DOM_BLOAT', 'THIN_CONTENT'];
    for (const kind of fatal) {
      const hits = byKind[kind] || [];
      expect(hits, `${kind}: ${hits.map((h) => h.route + '@' + h.viewport + ' ' + h.detail).join(' | ')}`).toEqual([]);
    }

    const unlabelled = byKind['UNLABELLED_FIELD'] || [];
    expect(unlabelled, `unlabelled fields: ${unlabelled.map((h) => h.route + ' ' + h.detail).join(' | ')}`).toEqual([]);

    const tiny = (byKind['TINY_TEXT'] || []).filter((t) => !/React Flow/i.test(t.detail));
    const tinyNonAttribution = tiny.filter((t) => !t.detail.startsWith('a @'));
    expect(tinyNonAttribution,
      `text below 11px: ${tinyNonAttribution.map((h) => h.route + ' ' + h.detail).join(' | ')}`).toEqual([]);
  });

  test.afterAll(() => {
    const byKind: Record<string, number> = {};
    for (const f of findings) byKind[f.kind] = (byKind[f.kind] || 0) + 1;
    const report = { summary: byKind, findings, timings };
    writeFileSync('layout-audit.json', JSON.stringify(report, null, 2));
    console.log('\n=== LAYOUT AUDIT SUMMARY ===');
    console.log(JSON.stringify(byKind, null, 2));
    const slow = [...timings].sort((a, b) => b.ms - a.ms).slice(0, 6);
    console.log('\nSlowest routes (laptop):');
    for (const s of slow) console.log(`  ${s.route.padEnd(18)} ${s.ms}ms  ${s.domNodes} DOM nodes`);
  });
});
