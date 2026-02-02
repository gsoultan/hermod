import { useState, useEffect, useRef } from 'react';
import { 
  Modal, ScrollArea, Stack, Box, Text, Group, Badge, 
  ActionIcon, Tooltip, TextInput, SegmentedControl, Code
} from '@mantine/core';
import { 
  IconTerminal2, IconTrash, IconSearch, IconFilter
} from '@tabler/icons-react';
import { useWorkflowStore } from '../store/useWorkflowStore';

interface LiveMessage {
  workflow_id: string;
  node_id: string;
  timestamp: string;
  data: any;
  is_error?: boolean;
  error?: string;
}

export function LiveStreamInspector({ opened, onClose, workflowId }: { 
  opened: boolean, 
  onClose: () => void,
  workflowId: string 
}) {
  const [messages, setMessages] = useState<LiveMessage[]>([]);
  const [paused, setPaused] = useState(false);
  const [search, setSearch] = useState('');
  const [filterNode, setFilterNode] = useState<string>('all');
  const wsRef = useRef<WebSocket | null>(null);
  const nodes = useWorkflowStore(state => state.nodes);

  useEffect(() => {
    if (opened && !paused) {
      const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
      const ws = new WebSocket(`${protocol}//${window.location.host}/api/ws/live?workflow_id=${workflowId}`);
      
      ws.onmessage = (event) => {
        const msg = JSON.parse(event.data);
        setMessages(prev => {
          const next = [msg, ...prev];
          return next.slice(0, 100); // Keep last 100
        });
      };

      wsRef.current = ws;
      return () => {
        ws.close();
      };
    } else if (wsRef.current) {
      wsRef.current.close();
      wsRef.current = null;
    }
  }, [opened, paused, workflowId]);

  const filteredMessages = messages.filter(m => {
    const matchesSearch = !search || JSON.stringify(m.data).toLowerCase().includes(search.toLowerCase());
    const matchesNode = filterNode === 'all' || m.node_id === filterNode;
    return matchesSearch && matchesNode;
  });

  const nodeOptions = [
    { label: 'All Nodes', value: 'all' },
    ...nodes.map(n => ({ label: n.data.label || n.id, value: n.id }))
  ];

  return (
    <Modal
      opened={opened}
      onClose={onClose}
      title={
        <Group gap="xs">
          <IconTerminal2 size="1.2rem" color="var(--mantine-color-blue-6)" />
          <Text fw={700}>Live Stream Inspector</Text>
          {paused && <Badge color="yellow" variant="light">Paused</Badge>}
        </Group>
      }
      size="xl"
      scrollAreaComponent={ScrollArea.Autosize}
    >
      <Stack gap="md">
        <Group justify="space-between">
          <Group gap="xs">
            <TextInput
              placeholder="Search data..."
              size="xs"
              leftSection={<IconSearch size="0.8rem" />}
              value={search}
              onChange={(e) => setSearch(e.currentTarget.value)}
              style={{ width: 200 }}
            />
            <SegmentedControl
              size="xs"
              data={[{ label: 'Live', value: 'live' }, { label: 'Paused', value: 'paused' }]}
              value={paused ? 'paused' : 'live'}
              onChange={(val) => setPaused(val === 'paused')}
            />
          </Group>
          <Group gap="xs">
            <Tooltip label="Clear stream">
              <ActionIcon variant="subtle" color="gray" onClick={() => setMessages([])}>
                <IconTrash size="1rem" />
              </ActionIcon>
            </Tooltip>
          </Group>
        </Group>

        <Box style={{ border: '1px solid var(--mantine-color-gray-3)', borderRadius: '8px', height: '500px', display: 'flex', flexDirection: 'column' }}>
           <Box p="xs" style={{ borderBottom: '1px solid var(--mantine-color-gray-3)' }}>
              <Group gap="xs">
                <IconFilter size="0.8rem" color="var(--mantine-color-gray-6)" />
                <Text size="xs" fw={700} c="dimmed">FILTER BY NODE</Text>
                <ScrollArea style={{ flex: 1 }}>
                  <Group gap={5} wrap="nowrap">
                    {nodeOptions.map(opt => (
                      <Badge 
                        key={opt.value}
                        variant={filterNode === opt.value ? 'filled' : 'light'}
                        color={filterNode === opt.value ? 'blue' : 'gray'}
                        style={{ cursor: 'pointer' }}
                        onClick={() => setFilterNode(opt.value)}
                      >
                        {opt.label}
                      </Badge>
                    ))}
                  </Group>
                </ScrollArea>
              </Group>
           </Box>

           <ScrollArea style={{ flex: 1 }} p="md">
              <Stack gap="sm">
                {filteredMessages.length === 0 && (
                  <Box py="xl" style={{ textAlign: 'center' }}>
                    <Text c="dimmed" size="sm">No live messages yet. Start the workflow to see data.</Text>
                  </Box>
                )}
                {filteredMessages.map((msg, i) => (
                  <Box 
                    key={i} 
                    p="xs" 
                    style={{ 
                      borderRadius: '4px', 
                      background: msg.is_error ? 'var(--mantine-color-red-0)' : 'var(--mantine-color-gray-0)',
                      border: `1px solid ${msg.is_error ? 'var(--mantine-color-red-2)' : 'var(--mantine-color-gray-2)'}`
                    }}
                  >
                    <Group justify="space-between" mb={4}>
                      <Group gap="xs">
                        <Badge size="xs" color="blue">{nodes.find(n => n.id === msg.node_id)?.data.label || msg.node_id}</Badge>
                        <Text size="xs" c="dimmed">{new Date(msg.timestamp).toLocaleTimeString()}</Text>
                      </Group>
                      {msg.is_error && <Badge size="xs" color="red">ERROR</Badge>}
                    </Group>
                    <Code block color={msg.is_error ? 'red' : 'gray'} style={{ fontSize: '10px' }}>
                      {JSON.stringify(msg.data, null, 2)}
                    </Code>
                    {msg.error && (
                      <Text size="xs" c="red" mt={4} fw={600}>Error: {msg.error}</Text>
                    )}
                  </Box>
                ))}
              </Stack>
           </ScrollArea>
        </Box>
      </Stack>
    </Modal>
  );
}
