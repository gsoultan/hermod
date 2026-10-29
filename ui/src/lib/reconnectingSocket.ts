const MAX_RECONNECT_DELAY = 30000;
const BASE_RECONNECT_DELAY = 1000;

/**
 * connectWithReconnect opens a WebSocket and transparently re-establishes the
 * connection (with capped exponential backoff) whenever it closes unexpectedly.
 * It returns a teardown function that permanently stops reconnection and closes
 * the active socket. onConnection, when given, hears each open and each close.
 * onSocket, when given, is handed each socket as it is opened and null once
 * torn down, for a caller that sends as well as listens.
 */
export function connectWithReconnect(
  buildUrl: () => string,
  onMessage: (event: MessageEvent) => void,
  onConnection?: (open: boolean) => void,
  onSocket?: (ws: WebSocket | null) => void,
): () => void {
  let ws: WebSocket | null = null;
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  let attempt = 0;
  let closed = false;

  const open = () => {
    if (closed) return;

    ws = new WebSocket(buildUrl());
    onSocket?.(ws);

    ws.onmessage = onMessage;

    ws.onopen = () => {
      attempt = 0;
      onConnection?.(true);
    };

    ws.onclose = () => {
      if (closed) return;
      onConnection?.(false);
      // Reconnect with capped exponential backoff and jitter to avoid a
      // thundering herd of reconnect attempts.
      const delay = Math.min(
        BASE_RECONNECT_DELAY * 2 ** attempt,
        MAX_RECONNECT_DELAY,
      );
      attempt += 1;
      const jitter = delay * (0.8 + Math.random() * 0.4);
      reconnectTimer = setTimeout(open, jitter);
    };

    ws.onerror = () => {
      // Force a close so the onclose handler schedules a reconnect.
      ws?.close();
    };
  };

  open();

  return () => {
    closed = true;
    if (reconnectTimer) clearTimeout(reconnectTimer);
    if (ws) {
      ws.onclose = null;
      ws.onerror = null;
      ws.close();
    }
    onSocket?.(null);
  };
}
