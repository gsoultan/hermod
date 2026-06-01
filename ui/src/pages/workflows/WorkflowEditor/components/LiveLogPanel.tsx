import { useRef, useEffect } from 'react';
import { 
  Paper, Group, Text, Badge, ActionIcon, ScrollArea, Stack 
} from '@mantine/core';
import { 
  IconChevronDown, IconChevronUp, IconClearAll, IconPlayerPlay, IconPlayerPause 
} from '@tabler/icons-react';
import { useWorkflowStore } from '../store/useWorkflowStore';
import { useShallow } from 'zustand/react/shallow';
import { formatTime } from '@/utils/dateUtils';
import type { LogEntry } from '@/types';

interface LiveLogPanelProps {
  workflowId: string;
  active: boolean;
  onTraceClick: (msgId: string) => void;
  onErrorClick: (log: LogEntry) => void;
}

export const LiveLogPanel = ({ active, onTraceClick, onErrorClick }: LiveLogPanelProps) => {
  const logScrollRef = useRef<HTMLDivElement>(null);
  
  const { 
    logs, logsOpened, logsPaused, setLogs, setLogsOpened, setLogsPaused 
  } = useWorkflowStore(useShallow(state => ({
    logs: state.logs,
    logsOpened: state.logsOpened,
    logsPaused: state.logsPaused,
    setLogs: state.setLogs,
    setLogsOpened: state.setLogsOpened,
    setLogsPaused: state.setLogsPaused,
  })));

  // Auto-scroll to bottom when new logs arrive
  useEffect(() => {
    if (logsOpened && !logsPaused && logScrollRef.current) {
      logScrollRef.current.scrollTo({ top: logScrollRef.current.scrollHeight, behavior: 'smooth' });
    }
  }, [logs, logsOpened, logsPaused]);

  return (
    <Paper withBorder radius="md" h={logsOpened ? 250 : 40} style={{ display: 'flex', flexDirection: 'column', transition: 'height 0.2s ease' }}>
       <Group justify="space-between" px="sm" h={40} style={{ borderBottom: logsOpened ? '1px solid var(--mantine-color-gray-2)' : 'none', cursor: 'pointer' }} onClick={() => setLogsOpened(!logsOpened)}>
          <Group gap="xs">
             {logsOpened ? <IconChevronDown size="1rem" /> : <IconChevronUp size="1rem" />}
             <Text size="sm" fw={600}>Live Workflow Logs</Text>
             {active && <Badge size="xs" color="green" variant="dot">Streaming</Badge>}
          </Group>
          <Group gap="xs">
             <ActionIcon aria-label="Clear logs" variant="subtle" size="sm" color="gray" onClick={(e) => { e.stopPropagation(); setLogs([]); }}>
                <IconClearAll size="1rem" />
             </ActionIcon>
             <ActionIcon aria-label={logsPaused ? "Resume logs" : "Pause logs"} variant="subtle" size="sm" color={logsPaused ? 'orange' : 'gray'} onClick={(e) => { e.stopPropagation(); setLogsPaused(!logsPaused); }}>
                {logsPaused ? <IconPlayerPlay size="1rem" /> : <IconPlayerPause size="1rem" />}
             </ActionIcon>
          </Group>
       </Group>
       {logsOpened && (
          <ScrollArea style={{ flex: 1 }} p="xs" viewportRef={logScrollRef}>
             <Stack gap={4}>
                {logs.map((log: LogEntry, i: number) => (
                   <Group 
                      key={i} 
                      gap="xs" 
                      wrap="nowrap" 
                      align="flex-start" 
                      style={{ 
                        cursor: log.data ? 'pointer' : 'default',
                        padding: '2px 4px',
                        borderRadius: '4px'
                      }}
                      onClick={() => {
                        if (log.data) {
                          let msgId = log.data;
                          if (msgId.includes('message_id: ')) {
                            const match = msgId.match(/message_id: ([^,]+)/);
                            if (match) msgId = match[1].trim();
                          }
                          onTraceClick(msgId);
                        } else if (log.level === 'ERROR') {
                          onErrorClick(log);
                        }
                      }}
                   >
                      <Text size="xs" c="dimmed" style={{ whiteSpace: 'nowrap', fontFamily: 'monospace' }}>
                         {formatTime(log.timestamp)}
                      </Text>
                      <Badge size="xs" color={log.level === 'ERROR' ? 'red' : log.level === 'WARN' ? 'orange' : 'blue'} variant="light" style={{ minWidth: 50 }}>
                         {log.level}
                      </Badge>
                      <Text size="xs" style={{ wordBreak: 'break-all', fontFamily: 'monospace' }}>
                         {log.message}
                         {log.data && <Badge size="xs" ml="xs" variant="outline" color="blue">Trace</Badge>}
                      </Text>
                   </Group>
                ))}
                {logs.length === 0 && (
                   <Text size="xs" c="dimmed" ta="center" py="xl">No logs yet.</Text>
                )}
             </Stack>
          </ScrollArea>
       )}
    </Paper>
  );
};
