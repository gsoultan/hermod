import { test, expect } from '@playwright/test';
import { E2E_USER, E2E_PASS } from './support/auth';

/**
 * Streams authenticate with the session cookie.
 *
 * The UI used to put the session JWT in every WebSocket URL, which forced a copy
 * of a 24-hour token into localStorage where any XSS could read it. It turned
 * out no credential was needed there at all: a browser sends the HttpOnly
 * session cookie on a same-origin WebSocket handshake (RFC 6455 §4.1).
 *
 * That claim is the load-bearing one. If it is wrong, every live view in the UI
 * silently goes dark — the socket closes, no frames arrive, and nothing throws.
 * A server-side unit test cannot check it, because the question is what the
 * *browser* sends. So it is checked here, in a real browser, against a real
 * server.
 *
 * The two assertions are deliberately paired:
 *
 *  - frames actually arrive, proving the handshake was authenticated; and
 *  - no stream URL carries a credential, proving it was the cookie that did it.
 *
 * Either alone is passable for the wrong reason.
 */

interface SocketRecord {
  url: string;
  frames: number;
  closed: boolean;
}

test.describe('Stream authentication', () => {
  test('WebSockets authenticate by cookie and carry no credential in the URL', async ({ page }) => {
    const sockets: SocketRecord[] = [];

    // Attach before any navigation: sockets opened during login would otherwise
    // be missed, and those are precisely the ones under test.
    page.on('websocket', (ws) => {
      const record: SocketRecord = { url: ws.url(), frames: 0, closed: false };
      sockets.push(record);
      ws.on('framereceived', () => {
        record.frames += 1;
      });
      ws.on('close', () => {
        record.closed = true;
      });
    });

    await page.goto('/login');
    await page.getByPlaceholder('Your username').fill(E2E_USER);
    await page.getByPlaceholder('Your password').fill(E2E_PASS);
    await page.click('button:has-text("Sign In")');
    await expect(page).not.toHaveURL(/login/, { timeout: 30000 });

    // The overview page opens the dashboard socket; the shell opens its own.
    await expect(page.getByText('Hermod', { exact: true }).first()).toBeVisible({
      timeout: 15000,
    });

    const streamSockets = () => sockets.filter((s) => s.url.includes('/api/ws/'));

    // A socket must open at all. If the server rejected the handshake this stays
    // empty or the socket closes immediately.
    await expect
      .poll(() => streamSockets().length, { timeout: 20000 })
      .toBeGreaterThan(0);

    // And it must actually receive data. An unauthenticated handshake is
    // refused, so frames arriving is the proof that the cookie was accepted.
    await expect
      .poll(() => streamSockets().filter((s) => s.frames > 0).length, {
        timeout: 30000,
        message:
          'No WebSocket frames arrived. If the handshake is being rejected, the ' +
          'session cookie is not reaching the server and the UI has no other ' +
          'credential to offer — every live view is dead.',
      })
      .toBeGreaterThan(0);

    // Nothing may smuggle a credential back into the URL.
    for (const socket of streamSockets()) {
      expect(
        socket.url,
        'a stream URL must not carry a credential: URLs are logged by the ' +
          'server, every proxy in between, and the browser history',
      ).not.toContain('token=');
    }
  });

  test('the workflow detail streams open without a URL credential', async ({ page }) => {
    const sockets: SocketRecord[] = [];
    page.on('websocket', (ws) => {
      const record: SocketRecord = { url: ws.url(), frames: 0, closed: false };
      sockets.push(record);
      ws.on('framereceived', () => {
        record.frames += 1;
      });
      ws.on('close', () => {
        record.closed = true;
      });
    });

    await page.goto('/login');
    await page.getByPlaceholder('Your username').fill(E2E_USER);
    await page.getByPlaceholder('Your password').fill(E2E_PASS);
    await page.click('button:has-text("Sign In")');
    await expect(page).not.toHaveURL(/login/, { timeout: 30000 });

    await page.goto('/workflows');
    await expect(page).toHaveURL(/\/workflows/, { timeout: 20000 });

    // Open the first workflow if the instance has one. With an empty instance
    // there is nothing to stream, so the per-workflow sockets are out of reach
    // and the shell sockets already covered above are all this can assert.
    const firstWorkflow = page.locator('a[href^="/workflows/"]').first();
    if ((await firstWorkflow.count()) > 0) {
      await firstWorkflow.click();
      await expect(page).toHaveURL(/\/workflows\/[^/]+$/, { timeout: 20000 });
      await page.waitForTimeout(3000);
    }

    for (const socket of sockets.filter((s) => s.url.includes('/api/ws/'))) {
      expect(socket.url, 'a stream URL must not carry a credential').not.toContain('token=');
    }
  });
});
