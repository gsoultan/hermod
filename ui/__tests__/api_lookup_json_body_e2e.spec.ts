import { test, expect } from '@playwright/test';
import http from 'node:http';
import type { AddressInfo } from 'node:net';
import { login, apiRequest } from './support/auth';

/**
 * An api_lookup with a JSON body failed against a real JSON API.
 *
 * The body went out with no Content-Type, so an endpoint that requires
 * application/json refused it, and the error said only "returned status 415".
 * Tokens were pasted in as raw text, so a jsonb field in quotes arrived as its
 * JSON text inside a string -- not JSON at all -- and so did any value holding
 * a quote.
 *
 * Driven from storage through the real editor: the node config is saved, the
 * node is opened, and its Live Preview makes the call. The endpoint is this
 * test's own, and it is as strict as a real JSON API.
 */

type Received = { contentType: string | undefined; body: string };

// A JSON API that insists on being sent JSON: 415 without the content type,
// 400 on a body it cannot parse, and a created session otherwise.
async function strictJSONEndpoint(): Promise<{ url: string; received: Received[]; close: () => Promise<void> }> {
  const received: Received[] = [];
  const server = http.createServer((req, res) => {
    let body = '';
    req.on('data', (chunk) => { body += chunk; });
    req.on('end', () => {
      received.push({ contentType: req.headers['content-type'], body });
      const reply = (status: number, obj: unknown) => {
        res.writeHead(status, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify(obj));
      };
      if ((req.headers['content-type'] || '').split(';')[0].trim() !== 'application/json') {
        return reply(415, { error: 'Content-Type must be application/json' });
      }
      try {
        JSON.parse(body);
      } catch (e) {
        return reply(400, { error: `request body is not valid JSON: ${(e as Error).message}` });
      }
      reply(201, { id: 'sess-e2e', accepted: true });
    });
  });
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
  const { port } = server.address() as AddressInfo;
  return {
    url: `http://127.0.0.1:${port}/sessions`,
    received,
    close: () => new Promise<void>((resolve) => server.close(() => resolve())),
  };
}

// The operator's body, verbatim, plus a jsonb field sent whole.
const BODY = `{
  "created_by_id": "{{.after.user_id}}",
  "profile": "{{.after.profile}}",
  "sessions": [
    {
      "user_id": "{{.after.user_id}}",
      "duration": 604800000000000,
      "scope": {
        "entity_id": "{{.after.entity_id}}",
        "entity": "{{.after.entity_type}}",
        "rule_ids": [
          "019a43e3-f5d7-7bf0-b559-fd3b55215ca7"
        ]
      }
    }
  ]
}`;

// The row the node previews with: a jsonb column, and a value holding a quote.
const ROW = {
  operation: 'snapshot',
  table: 'memberships',
  after: {
    user_id: '019a43e3-0000-7000-8000-000000000001',
    entity_id: '019a43e3-0000-7000-8000-0000000000e1',
    entity_type: 'region "north"',
    profile: { name: 'Ada', roles: ['owner', 'admin'] },
  },
};

const created: { workflows: string[]; sources: string[] } = { workflows: [], sources: [] };

test.afterEach(async ({ page }) => {
  for (const id of created.workflows.splice(0)) {
    await apiRequest(page, `/api/workflows/${id}`, { method: 'DELETE' }).catch(() => {});
  }
  for (const id of created.sources.splice(0)) {
    await apiRequest(page, `/api/sources/${id}`, { method: 'DELETE' }).catch(() => {});
  }
});

test('api_lookup sends its JSON body as JSON, jsonb included', async ({ page }) => {
  test.setTimeout(120000);
  const endpoint = await strictJSONEndpoint();
  try {
    await login(page);
    const stamp = Date.now();
    const post = async (path: string, body: unknown) => {
      const res = await apiRequest(page, path, { method: 'POST', body });
      if (res.status >= 400) throw new Error(`fixture setup failed: POST ${path} -> ${res.status} ${res.body}`);
      return JSON.parse(res.body);
    };

    // A workflow has to hold a source; the lookup node previews from its own
    // lastSample, as a node with nothing wired into it does.
    const source = await post('/api/sources', {
      name: `apl-json-src-${stamp}`, type: 'webhook', vhost: 'default', active: false,
      config: { path: `/apl-json-${stamp}` },
    });
    created.sources.push(source.id);
    const workflow = await post('/api/workflows', {
      name: `apl-json-${stamp}`, vhost: 'default', active: false,
      nodes: [
        { id: 'n-src', type: 'source', ref_id: source.id, x: 100, y: 100 },
        {
          id: 'n-api', type: 'transformation', x: 400, y: 100,
          config: {
            transType: 'api_lookup', method: 'POST', url: endpoint.url, body: BODY,
            targetField: 'session', ttl: '0', lastSample: ROW,
          },
        },
      ],
      edges: [],
    });
    created.workflows.push(workflow.id);

    await page.goto(`/workflows/${workflow.id}/edit`);
    await page.waitForLoadState('networkidle');
    const nodes = page.locator('.react-flow__node');
    await expect(nodes).toHaveCount(2, { timeout: 30000 });
    await nodes.nth(1).dblclick();
    await expect(page.getByTestId('available-fields-panel')).toBeVisible({ timeout: 20000 });

    // The Live Preview runs the lookup on open. Its result carries what the
    // endpoint answered, which it only does once the endpoint accepted the call.
    await expect(page.locator('pre', { hasText: 'sess-e2e' })).toBeVisible({ timeout: 20000 });

    const last = endpoint.received.at(-1);
    expect(last, 'the endpoint was never called').toBeDefined();
    expect(last!.contentType).toBe('application/json');
    const sent = JSON.parse(last!.body);
    expect(sent.created_by_id).toBe(ROW.after.user_id);
    expect(sent.profile, 'the jsonb field arrived as text, not as itself').toEqual(ROW.after.profile);
    expect(sent.sessions[0].scope.entity).toBe('region "north"');
    // The number literal went out as written, not through a float.
    expect(last!.body).toContain('"duration": 604800000000000');
  } finally {
    await endpoint.close();
  }
});
