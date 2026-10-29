import { useState, useEffect, useRef } from 'react';
import { 
  Paper, Stack, Group, Text, Title, Badge, ScrollArea, Box, 
  Button, Code, Alert, ThemeIcon
} from '@mantine/core';
import { 
  IconPlayerPlay, IconPlayerPause, IconStepInto, IconTrash, 
  IconCircleX, IconActivity, IconTerminal2
} from '@tabler/icons-react';
import { formatDateTime } from '@/utils/dateUtils';
import { AgentRunDetails } from '@/components/workflow/AgentTranscript/AgentRunDetails';

interface DebugEvent {
  type: string;
  msg_id: string;
  node_id: string;
  timestamp: string;
  data: any;
  error?: string;
  /** "paused", or how a pause ended. The engine sends it as `state`. */
  state?: string;
  status?: string;
  /** Set when the message is held on an edge breakpoint rather than before a node. */
  edge_id?: string;
  /** What a command names to answer the pause. */
  pause_id?: string;
}

/** Whether an event is a message held now. */
const isPaused = (ev: DebugEvent) => (ev.state ?? ev.status) === 'paused';

/**
 * Most events the debugger holds. Every event used to be kept for as long as
 * the panel stayed open, so a busy pipeline grew the list without end; the
 * oldest are dropped once this many have arrived.
 */
export const DEBUG_EVENT_LIMIT = 500;

export function WorkflowDebugger({ workflowId }: { workflowId: string }) {
  const [events, setEvents] = useState<DebugEvent[]>([]);
  const [isConnected, setIsConnected] = useState(false);
  const ws = useRef<WebSocket | null>(null);
  const scrollRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const host = window.location.host;
    // This pointed at /api/v1/debug, which is not a registered route — the only
    // debugger endpoint is /api/ws/debugger — so this socket never connected and
    // the panel stayed on "Disconnected" forever.
    //
    // Authenticated by the HttpOnly session cookie, sent on the same-origin
    // handshake, so there is no credential in the URL.
    const socket = new WebSocket(
      `${protocol}//${host}/api/ws/debugger?workflow_id=${encodeURIComponent(workflowId)}`,
    );

    socket.onopen = () => setIsConnected(true);
    socket.onclose = () => setIsConnected(false);
    socket.onmessage = (event) => {
      const ev = JSON.parse(event.data);
      setEvents((prev) => {
        const next = [...prev, ev];
        return next.length > DEBUG_EVENT_LIMIT ? next.slice(next.length - DEBUG_EVENT_LIMIT) : next;
      });
    };

    ws.current = socket;
    return () => socket.close();
  }, [workflowId]);

  const sendCommand = (ev: DebugEvent, action: string) => {
    ws.current?.send(JSON.stringify({ action, msg_id: ev.msg_id, pause_id: ev.pause_id ?? ev.msg_id }));
  };

  const clearEvents = () => setEvents([]);

  useEffect(() => {
    if (scrollRef.current) {
      scrollRef.current.scrollTo({ top: scrollRef.current.scrollHeight, behavior: 'smooth' });
    }
  }, [events]);

  return (
    <Stack h="100%" gap="md" p="md">
      <Group justify="space-between">
        <Box>
          <Title order={4}>Interactive Debugger</Title>
          <Text size="sm" c="dimmed">Pause, step, and inspect messages as they flow through the pipeline.</Text>
        </Box>
        <Group>
          <Badge color={isConnected ? 'green' : 'red'} variant="dot">
            {isConnected ? 'Connected' : 'Disconnected'}
          </Badge>
          <Button 
            variant="light" 
            size="xs" 
            leftSection={<IconTrash size="0.8rem" />} 
            onClick={clearEvents}
            disabled={events.length === 0}
          >
            Clear
          </Button>
        </Group>
      </Group>

      <Paper withBorder style={{ flex: 1, display: 'flex', flexDirection: 'column', overflow: 'hidden' }}>
        <ScrollArea viewportRef={scrollRef} style={{ flex: 1 }} p="md">
          {events.length === 0 ? (
            <Stack h={300} align="center" justify="center" gap="xs">
              <IconTerminal2 size="3rem" color="var(--mantine-color-gray-3)" />
              <Text c="dimmed">No debug events yet. Send some data to start debugging.</Text>
            </Stack>
          ) : (
            <Stack gap="sm">
              {events.map((ev, idx) => (
                <Paper key={idx} withBorder p="sm" radius="md" bg={isPaused(ev) ? 'orange.0' : 'transparent'}>
                  <Stack gap="xs">
                    <Group justify="space-between">
                      <Group gap="xs">
                        <ThemeIcon size="sm" color={ev.error ? 'red' : 'blue'} variant="light">
                          {isPaused(ev) ? <IconPlayerPause size={14} /> : <IconActivity size={14} />}
                        </ThemeIcon>
                        <Text fw={700} size="sm">{ev.edge_id ? `Edge: ${ev.edge_id} → ${ev.node_id}` : `Node: ${ev.node_id}`}</Text>
                        {ev.type && <Badge size="xs" variant="outline">{ev.type}</Badge>}
                        {isPaused(ev) && <Badge color="orange" size="xs">PAUSED</Badge>}
                      </Group>
                      <Text size="xs" c="dimmed">{formatDateTime(ev.timestamp)}</Text>
                    </Group>

                    <Text size="xs" c="dimmed">Message ID: <Code>{ev.msg_id}</Code></Text>

                    {ev.data && <AgentRunDetails data={ev.data} />}

                    {ev.data && (
                      <Box>
                        <Text size="xs" fw={700} mb={4}>PAYLOAD</Text>
                        <Code block>{JSON.stringify(ev.data, null, 2)}</Code>
                      </Box>
                    )}

                    {ev.error && (
                      <Alert color="red" icon={<IconCircleX size="1rem" />} p="xs">
                        {ev.error}
                      </Alert>
                    )}

                    {isPaused(ev) && (
                      <Group gap="xs" mt="xs">
                        <Button 
                          size="xs" 
                          variant="filled" 
                          color="green" 
                          leftSection={<IconPlayerPlay size={14} />}
                          onClick={() => sendCommand(ev, 'resume')}
                        >
                          Resume
                        </Button>
                        <Button 
                          size="xs" 
                          variant="outline" 
                          color="blue" 
                          leftSection={<IconStepInto size={14} />}
                          onClick={() => sendCommand(ev, 'step')}
                        >
                          Step
                        </Button>
                      </Group>
                    )}
                  </Stack>
                </Paper>
              ))}
            </Stack>
          )}
        </ScrollArea>
      </Paper>
    </Stack>
  );
}
