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
import { getToken } from '@/auth/storage';

interface DebugEvent {
  type: string;
  msg_id: string;
  node_id: string;
  timestamp: string;
  data: any;
  error?: string;
  status?: string;
}

export function WorkflowDebugger({ workflowId }: { workflowId: string }) {
  const [events, setEvents] = useState<DebugEvent[]>([]);
  const [isConnected, setIsConnected] = useState(false);
  const ws = useRef<WebSocket | null>(null);
  const scrollRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const host = window.location.host;
    const token = getToken();
    const socket = new WebSocket(`${protocol}//${host}/api/v1/debug?workflow_id=${workflowId}&token=${token}`);

    socket.onopen = () => setIsConnected(true);
    socket.onclose = () => setIsConnected(false);
    socket.onmessage = (event) => {
      const ev = JSON.parse(event.data);
      setEvents((prev) => [...prev, ev]);
    };

    ws.current = socket;
    return () => socket.close();
  }, [workflowId]);

  const sendCommand = (msgId: string, action: string) => {
    ws.current?.send(JSON.stringify({ action, msg_id: msgId }));
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
                <Paper key={idx} withBorder p="sm" radius="md" bg={ev.status === 'paused' ? 'orange.0' : 'transparent'}>
                  <Stack gap="xs">
                    <Group justify="space-between">
                      <Group gap="xs">
                        <ThemeIcon size="sm" color={ev.error ? 'red' : 'blue'} variant="light">
                          {ev.status === 'paused' ? <IconPlayerPause size={14} /> : <IconActivity size={14} />}
                        </ThemeIcon>
                        <Text fw={700} size="sm">Node: {ev.node_id}</Text>
                        <Badge size="xs" variant="outline">{ev.type}</Badge>
                        {ev.status === 'paused' && <Badge color="orange" size="xs">PAUSED</Badge>}
                      </Group>
                      <Text size="xs" c="dimmed">{formatDateTime(ev.timestamp)}</Text>
                    </Group>

                    <Text size="xs" c="dimmed">Message ID: <Code>{ev.msg_id}</Code></Text>

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

                    {ev.status === 'paused' && (
                      <Group gap="xs" mt="xs">
                        <Button 
                          size="xs" 
                          variant="filled" 
                          color="green" 
                          leftSection={<IconPlayerPlay size={14} />}
                          onClick={() => sendCommand(ev.msg_id, 'resume')}
                        >
                          Resume
                        </Button>
                        <Button 
                          size="xs" 
                          variant="outline" 
                          color="blue" 
                          leftSection={<IconStepInto size={14} />}
                          onClick={() => sendCommand(ev.msg_id, 'step')}
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
