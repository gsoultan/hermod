import { useEffect } from 'react';
import { useWorkflowStore } from '../store/useWorkflowStore';
import { connectWithReconnect } from '@/lib/reconnectingSocket';
import { isEngineLive } from '../live/liveHealth';
import { applyDebuggerEvent, type DebuggerEvent } from '../live/edgePauses';
import { setDebuggerSocket } from '../live/breakpointSocket';

function getWsProtocol(): string {
  return window.location.protocol === 'https:' ? 'wss:' : 'ws:';
}

/**
 * Attaches the editor to the workflow's debugger, in breakpoints mode, while
 * the workflow runs and has an edge breakpoint, and keeps the messages held on
 * them in the store.
 *
 * Attaching is what makes the engine stop at a breakpoint: with nobody to
 * answer, it lets messages through. Breakpoints mode stops them only there,
 * not before every node as the Debugger tab does.
 */
export function useEdgeBreakpoints(id: string) {
  const running = useWorkflowStore((s) => isEngineLive(s.workflowStatus));
  const hasBreakpoint = useWorkflowStore((s) => s.edges.some((e) => !!(e.data as { breakpoint?: unknown } | undefined)?.breakpoint));

  useEffect(() => {
    if (!id || id === 'new' || !running || !hasBreakpoint) return;

    // Authenticated by the HttpOnly session cookie on the same-origin handshake.
    const buildUrl = () =>
      `${getWsProtocol()}//${window.location.host}/api/ws/debugger?workflow_id=${encodeURIComponent(id)}&mode=breakpoints`;

    const onMessage = (event: MessageEvent) => {
      try {
        const ev = JSON.parse(event.data) as DebuggerEvent;
        const pauses = useWorkflowStore.getState().edgePauses;
        const next = applyDebuggerEvent(pauses, ev);
        if (next !== pauses) useWorkflowStore.setState({ edgePauses: next });
      } catch {
        // Ignore malformed debugger frames.
      }
    };

    // A new connection is sent every pause still held, so what was shown
    // before it is dropped rather than left to go stale.
    const onConnection = (open: boolean) => {
      if (!open) useWorkflowStore.setState({ edgePauses: {} });
    };

    const close = connectWithReconnect(buildUrl, onMessage, onConnection, setDebuggerSocket);
    return () => {
      close();
      setDebuggerSocket(null);
      useWorkflowStore.setState({ edgePauses: {} });
    };
  }, [id, running, hasBreakpoint]);
}
