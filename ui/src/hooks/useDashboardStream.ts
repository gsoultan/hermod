import { useCallback, useEffect, useRef, useState } from 'react';
import { apiFetch } from '@/api';

export interface DashboardStats {
  active_sources: number;
  active_sinks: number;
  active_workflows: number;
  total_processed: number;
  total_lag: number;
  total_errors: number;
  failed_workflows: number;
  uptime: number;
  active_workers: number;
  total_workflows: number;
  total_sources: number;
  total_sinks: number;
  throughput: number;
  error_rate: number;
  avg_latency_ms: number;
  backpressure: number;
  circuit_breakers_open: number;
}

export interface DashboardSample {
  timestamp: string;
  vhost: string;
  throughput: number;
  total_processed: number;
  total_errors: number;
  total_lag: number;
  error_rate: number;
  avg_latency_ms: number;
  active_workflows: number;
  active_workers: number;
}

export const EMPTY_STATS: DashboardStats = {
  active_sources: 0,
  active_sinks: 0,
  active_workflows: 0,
  total_processed: 0,
  total_lag: 0,
  total_errors: 0,
  failed_workflows: 0,
  uptime: 0,
  active_workers: 0,
  total_workflows: 0,
  total_sources: 0,
  total_sinks: 0,
  throughput: 0,
  error_rate: 0,
  avg_latency_ms: 0,
  backpressure: 0,
  circuit_breakers_open: 0,
};

/**
 * Points kept on the throughput chart.
 *
 * The server samples every five seconds, so this is twenty minutes of trend —
 * enough to see a spike start and end, and bounded so a tab left open
 * overnight does not accumulate an array all night.
 */
export const MAX_CHART_POINTS = 240;

/** How much history to ask for on load, matching MAX_CHART_POINTS. */
const HISTORY_WINDOW = '20m';

/** Reconnect backoff, milliseconds. Jittered so tabs do not resynchronise. */
const BACKOFF_MS = [1_000, 2_000, 4_000, 8_000, 15_000, 30_000];

/**
 * `live` once a frame has arrived; `offline` when the socket is down and a
 * reconnect is pending. The distinction matters on screen: an operator needs
 * to know whether the numbers in front of them are current or frozen.
 */
export type StreamState = 'connecting' | 'live' | 'offline';

/**
 * The dashboard's live numbers, its chart history, and whether it is current.
 *
 * This replaces an effect on DashboardPage that had three problems, all of
 * which pointed the same way — a dashboard that had stopped updating looked
 * exactly like a system with nothing happening:
 *
 *  - the socket was opened once with no `onclose` or `onerror`, so a dropped
 *    connection was never re-established and never surfaced;
 *  - the chart lived entirely in React state, so every reload threw the trend
 *    away and restarted from a flat line;
 *  - every fetch ended in `.catch(console.error)`, so an API returning 500
 *    rendered as a tidy dashboard full of zeros.
 *
 * The socket now reconnects with backoff, the chart is seeded from persisted
 * history, and failures are returned rather than swallowed.
 */
export function useDashboardStream(vhost: string) {
  const [stats, setStats] = useState<DashboardStats>(EMPTY_STATS);
  const [series, setSeries] = useState<number[]>([]);
  const [connection, setConnection] = useState<StreamState>('connecting');
  const [error, setError] = useState<string | null>(null);

  // Last seen counter, used to derive a rate when the server reports no
  // throughput of its own. Held in a ref so updating it never costs a render.
  const lastCount = useRef<{ time: number; count: number } | null>(null);

  const pushPoint = useCallback((value: number) => {
    setSeries((prev) => {
      const next = prev.length >= MAX_CHART_POINTS ? prev.slice(prev.length - MAX_CHART_POINTS + 1) : prev.slice();
      next.push(value);
      return next;
    });
  }, []);

  useEffect(() => {
    let cancelled = false;

    // Seed the chart from persisted history so a reload shows the trend that
    // was already there rather than starting from flat.
    apiFetch(`/api/dashboard/history?vhost=${encodeURIComponent(vhost)}&window=${HISTORY_WINDOW}`)
      .then((res) => {
        if (!res.ok) throw new Error(`history request failed (${res.status})`);
        return res.json();
      })
      .then((data: DashboardSample[]) => {
        if (cancelled || !Array.isArray(data)) return;
        setSeries(data.slice(-MAX_CHART_POINTS).map((s) => s.throughput ?? 0));
      })
      .catch(() => {
        // A missing chart history is not worth an error banner: the live
        // socket still populates the chart from here on. Leave it empty.
      });

    return () => {
      cancelled = true;
    };
  }, [vhost]);

  useEffect(() => {
    let cancelled = false;

    setStats(EMPTY_STATS);
    setError(null);
    setConnection('connecting');
    lastCount.current = null;

    // One snapshot up front so the cards are populated before the first
    // socket frame, and so an unreachable API is reported rather than
    // rendered as a dashboard full of zeros.
    apiFetch(`/api/dashboard/stats?vhost=${encodeURIComponent(vhost)}`)
      .then((res) => {
        if (!res.ok) throw new Error(`stats request failed (${res.status})`);
        return res.json();
      })
      .then((data: DashboardStats) => {
        if (cancelled) return;
        setStats(data);
        setError(null);
        lastCount.current = { time: Date.now(), count: data.total_processed };
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        setError(err instanceof Error ? err.message : 'Could not load dashboard stats');
      });

    let socket: WebSocket | null = null;
    let retry = 0;
    let reconnectTimer: ReturnType<typeof setTimeout> | null = null;

    const connect = () => {
      if (cancelled) return;

      // No token in the URL: the browser sends the HttpOnly hermod_session
      // cookie on a same-origin WebSocket handshake (RFC 6455 §4.1), which is
      // how every other stream here authenticates.
      const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
      socket = new WebSocket(
        `${protocol}//${window.location.host}/api/ws/dashboard?vhost=${encodeURIComponent(vhost)}`,
      );

      socket.onopen = () => {
        retry = 0;
        if (!cancelled) setConnection('live');
      };

      socket.onmessage = (event: MessageEvent) => {
        if (cancelled) return;
        let data: DashboardStats;
        try {
          data = JSON.parse(event.data) as DashboardStats;
        } catch {
          // Malformed frame. Drop it and keep the socket.
          return;
        }
        if (!data || typeof data !== 'object') return;

        setStats(data);
        setConnection('live');
        setError(null);

        const now = Date.now();
        const previous = lastCount.current;
        lastCount.current = { time: now, count: data.total_processed };

        // Prefer the server's own throughput. It comes from the engine's rate
        // tracker, which is the only thing that can distinguish "idle" from
        // "between samples".
        if (data.throughput > 0) {
          pushPoint(data.throughput);
          return;
        }

        // Otherwise derive a rate from the counter. This is what covers a
        // control plane running no engines of its own: throughput is a local
        // reading, but total_processed is the cluster's.
        if (!previous || previous.count === 0) return;
        const elapsed = (now - previous.time) / 1000;
        const delta = data.total_processed - previous.count;
        // A counter that went backwards means an engine restarted, not
        // negative traffic.
        if (elapsed <= 0 || delta < 0) return;
        pushPoint(delta / elapsed);
      };

      const onDown = () => {
        if (cancelled) return;
        setConnection('offline');
        const base = BACKOFF_MS[Math.min(retry, BACKOFF_MS.length - 1)];
        retry += 1;
        // Jitter so every open tab does not retry on the same tick.
        reconnectTimer = setTimeout(connect, base + Math.floor(Math.random() * 500));
      };

      socket.onclose = onDown;
      socket.onerror = () => socket?.close();
    };

    connect();

    return () => {
      cancelled = true;
      if (reconnectTimer) clearTimeout(reconnectTimer);
      if (socket) {
        // Drop the handlers first: close() fires onclose, which would
        // otherwise schedule a reconnect for a page that is going away.
        socket.onclose = null;
        socket.onerror = null;
        socket.close();
      }
    };
  }, [vhost, pushPoint]);

  return { stats, series, connection, error };
}
