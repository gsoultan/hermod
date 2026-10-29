/** A command answering one pause, as the debugger socket takes it. */
export interface DebuggerCommand {
  action: 'resume';
  msg_id: string;
  pause_id: string;
}

// The editor's one debugger socket, while it is open; useEdgeBreakpoints owns it.
let current: WebSocket | null = null;

/** Records the socket commands go out on, or null once it is gone. */
export function setDebuggerSocket(ws: WebSocket | null) {
  current = ws;
}

/** Sends a command, and reports whether there was an open socket to send it on. */
export function sendDebuggerCommand(cmd: DebuggerCommand): boolean {
  if (!current || current.readyState !== WebSocket.OPEN) return false;
  current.send(JSON.stringify(cmd));
  return true;
}
