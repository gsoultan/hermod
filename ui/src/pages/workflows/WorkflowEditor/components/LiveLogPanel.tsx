import { useRef, useEffect } from 'react';
import { 
  Paper, Group, Text, Badge, ActionIcon, ScrollArea, Stack, UnstyledButton
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

  // Follow new lines as they arrive. They are added at the top of the list,
  // so that is where to scroll; scrolling to the bottom, as this used to,
  // moved away from the line that had just arrived.
  useEffect(() => {
    if (logsOpened && !logsPaused && logScrollRef.current) {
      logScrollRef.current.scrollTo({ top: 0, behavior: 'smooth' });
    }
  }, [logs, logsOpened, logsPaused]);

  const openLog = (log: LogEntry) => {
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
  };

  return (
    <Paper
      withBorder
      radius="md"
      h={logsOpened ? 250 : 40}
      style={{
        // Floats along the bottom of the canvas instead of sitting below it as
        // an inline sibling, where it permanently cost 250px of canvas height
        // when open and 40px even when collapsed.
        position: 'absolute',
        left: 0,
        right: 0,
        bottom: 0,
        zIndex: 4,
        display: 'flex',
        flexDirection: 'column',
        transition: 'height 0.2s ease',
      }}
    >
       <Group justify="space-between" px="sm" h={40} style={{ borderBottom: logsOpened ? '1px solid var(--mantine-color-gray-2)' : 'none', cursor: 'pointer' }} onClick={() => setLogsOpened(!logsOpened)}>
          {/* A real button, so the panel folds from the keyboard and says
              whether it is open. No handler of its own: its click bubbles to
              the header, which stays clickable across its whole width. */}
          <UnstyledButton aria-expanded={logsOpened}>
            <Group gap="xs">
               {logsOpened ? <IconChevronDown size="1rem" /> : <IconChevronUp size="1rem" />}
               <Text size="sm" fw={600}>Live logs</Text>
               {active && <Badge size="xs" color="green" variant="dot">Streaming</Badge>}
               {!active && !logsOpened && <Text size="sm" c="dimmed" visibleFrom="sm">Turn the flow on to see events here.</Text>}
            </Group>
          </UnstyledButton>
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
                {logs.map((log: LogEntry, i: number) => {
                   // A row that opens something is a button: focusable, and
                   // pressed with Enter or Space as well as a click.
                   const actionable = Boolean(log.data) || log.level === 'ERROR';
                   return (
                   <Group 
                      key={i} 
                      gap="xs" 
                      wrap="nowrap" 
                      align="flex-start" 
                      role={actionable ? 'button' : undefined}
                      tabIndex={actionable ? 0 : undefined}
                      style={{ 
                        cursor: actionable ? 'pointer' : 'default',
                        padding: '2px 4px',
                        borderRadius: '4px'
                      }}
                      onClick={() => openLog(log)}
                      onKeyDown={(e) => {
                        if (!actionable || (e.key !== 'Enter' && e.key !== ' ')) return;
                        e.preventDefault();
                        openLog(log);
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
                   );
                })}
                {logs.length === 0 && (
                   <Text size="xs" c="dimmed" ta="center" py="xl">No logs yet.</Text>
                )}
             </Stack>
          </ScrollArea>
       )}
    </Paper>
  );
};
