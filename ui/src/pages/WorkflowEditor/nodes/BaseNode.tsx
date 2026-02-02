import { createContext, useContext, type ReactNode } from 'react';
import { Handle, Position } from 'reactflow';
import { Box, Text, useMantineColorScheme, ActionIcon, Tooltip } from '@mantine/core';
import { IconPlus, IconEye } from '@tabler/icons-react';
import { useWorkflowStore } from '../store/useWorkflowStore';

export const WorkflowContext = createContext<{
  onPlusClick: (nodeId: string, handleId: string | null) => void;
} | null>(null);

export const PlusHandle = ({ type, position, id, color, nodeId, style }: any) => {
  const context = useContext(WorkflowContext);
  return (
    <Handle 
      type={type} 
      position={position} 
      id={id}
      className="n8n-handle"
      style={{ 
        width: 20, 
        height: 20, 
        background: 'white',
        [position === Position.Right ? 'right' : position === Position.Left ? 'left' : position === Position.Bottom ? 'bottom' : 'top']: -10,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        border: `2px solid var(--mantine-color-${color}-6)`,
        boxShadow: '0 2px 4px rgba(0,0,0,0.1)',
        zIndex: 100,
        cursor: 'pointer',
        ['--handle-color' as any]: `var(--mantine-color-${color}-6)`,
        ...style
      }}
      onClick={(e) => {
        if (context) {
          e.stopPropagation();
          context.onPlusClick(nodeId, id);
        }
      }}
    >
      <IconPlus size="0.8rem" color={`var(--mantine-color-${color}-6)`} stroke={3} />
    </Handle>
  );
};

export const TargetHandle = ({ position, color, style }: any) => {
  return (
    <Handle 
      type="target" 
      position={position} 
      style={{ 
        width: 12, 
        height: 12, 
        background: 'white',
        [position === Position.Left ? 'left' : position === Position.Right ? 'right' : position === Position.Top ? 'top' : 'bottom']: -6,
        border: `2px solid var(--mantine-color-${color}-6)`,
        boxShadow: '0 2px 4px rgba(0,0,0,0.1)',
        zIndex: 10,
        ...style
      }} 
    />
  );
};

export const BaseNode = ({ id, type, color, icon: Icon, children, data }: { 
  id: string, 
  type: string, 
  color: string, 
  icon: any, 
  children: ReactNode, 
  data: any 
}) => {
  const { colorScheme } = useMantineColorScheme();
  const isDark = colorScheme === 'dark';
  const setSampleInspectorOpened = useWorkflowStore(state => state.setSampleInspectorOpened);
  const setSampleNodeId = useWorkflowStore(state => state.setSampleNodeId);
  const metric = useWorkflowStore(state => state.nodeMetrics[id]) ?? data.metric;
  const errorCount = useWorkflowStore(state => state.nodeErrorMetrics[id]) ?? data.errorCount;
  const sample = useWorkflowStore(state => state.nodeSamples[id]) ?? data.sample;
  const cbStatus = useWorkflowStore(state => state.sinkCBStatuses[data.ref_id]) ?? data.cbStatus;
  const bufferFill = useWorkflowStore(state => state.sinkBufferFill[data.ref_id]) ?? data.bufferFill;
  const sourceStatus = useWorkflowStore(state => state.sourceStatus);
  const sinkStatus = useWorkflowStore(state => state.sinkStatuses[data.ref_id]);
  const workflowDeadLetterCount = useWorkflowStore(state => state.workflowDeadLetterCount);

  const nodeStatus = type === 'Source' ? sourceStatus : (type === 'Sink' ? sinkStatus : null);

  const healthColor = errorCount > 0 ? (errorCount / (metric + errorCount) > 0.1 ? 'red' : 'orange') : color;
  const borderStyle = data.isDLQ ? 'dashed' : 'solid';
  const borderWidth = errorCount > 0 ? '3px' : '2px';
  
  // Reliability Indicators
  const cbOpen = cbStatus === 'open';
  const cbHalfOpen = cbStatus === 'half-open';
  const bufferHigh = bufferFill > 0.8;

  return (
    <Box
      style={{
        background: isDark ? 'var(--mantine-color-dark-6)' : 'white',
        border: `${borderWidth} ${borderStyle} var(--mantine-color-${cbOpen ? 'red' : healthColor}-6)`,
        borderRadius: '8px',
        padding: '12px',
        minWidth: '180px',
        boxShadow: cbOpen ? '0 0 15px var(--mantine-color-red-6)' : (errorCount > 0 ? `0 0 10px var(--mantine-color-${healthColor}-3)` : '0 4px 6px rgba(0,0,0,0.1)'),
        position: 'relative',
        opacity: data.testResult && data.testResult.status === 'FILTERED' ? 0.5 : 1,
      }}
    >
      {nodeStatus && nodeStatus !== 'running' && (
        <Box 
          style={{ 
            position: 'absolute', 
            top: -20, 
            right: 0,
            background: nodeStatus.startsWith('error') ? 'var(--mantine-color-red-6)' : 'var(--mantine-color-blue-6)',
            borderRadius: '4px',
            padding: '2px 8px',
            color: 'white',
            fontSize: '9px',
            fontWeight: 800,
            zIndex: 10,
            whiteSpace: 'nowrap',
            textTransform: 'uppercase'
          }}
        >
          {nodeStatus}
        </Box>
      )}
      {cbOpen && (
        <Box 
          style={{ 
            position: 'absolute', 
            top: -20, 
            left: '50%',
            transform: 'translateX(-50%)',
            background: 'var(--mantine-color-red-6)',
            borderRadius: '4px',
            padding: '2px 8px',
            color: 'white',
            fontSize: '9px',
            fontWeight: 800,
            zIndex: 10,
            whiteSpace: 'nowrap'
          }}
        >
          CIRCUIT BREAKER: OPEN
        </Box>
      )}
      {cbHalfOpen && (
        <Box 
          style={{ 
            position: 'absolute', 
            top: -20, 
            left: '50%',
            transform: 'translateX(-50%)',
            background: 'var(--mantine-color-orange-6)',
            borderRadius: '4px',
            padding: '2px 8px',
            color: 'white',
            fontSize: '9px',
            fontWeight: 800,
            zIndex: 10,
            whiteSpace: 'nowrap'
          }}
        >
          CIRCUIT BREAKER: HALF-OPEN
        </Box>
      )}
      {bufferFill > 0 && (
        <Box 
          style={{ 
            position: 'absolute', 
            top: -5, 
            right: 10,
            width: '40px',
            height: '4px',
            background: 'var(--mantine-color-gray-3)',
            borderRadius: '2px',
            overflow: 'hidden',
            zIndex: 10
          }}
        >
          <Box 
            style={{ 
              width: `${bufferFill * 100}%`,
              height: '100%',
              background: bufferHigh ? 'var(--mantine-color-red-6)' : 'var(--mantine-color-blue-6)',
              transition: 'width 0.3s ease'
            }}
          />
        </Box>
      )}
      {data.isDLQ && (
        <Box 
          style={{ 
            position: 'absolute', 
            top: -10, 
            left: 10,
            background: 'var(--mantine-color-orange-6)',
            borderRadius: '4px',
            padding: '2px 6px',
            color: 'white',
            fontSize: '9px',
            fontWeight: 800,
            zIndex: 10
          }}
        >
          DEAD LETTER SINK
        </Box>
      )}
      <Box style={{ display: 'flex', alignItems: 'center', gap: '8px', marginBottom: '8px' }}>
        <Box 
          style={{ 
            background: data.isDLQ ? 'var(--mantine-color-orange-1)' : `var(--mantine-color-${color}-1)`, 
            padding: '4px', 
            borderRadius: '4px',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center'
          }}
        >
          <Icon size="1.2rem" color={data.isDLQ ? 'var(--mantine-color-orange-6)' : `var(--mantine-color-${color}-6)`} />
        </Box>
        <Box style={{ flex: 1, overflow: 'hidden' }}>
          <Text size="xs" fw={700} c="dimmed" style={{ textTransform: 'uppercase', letterSpacing: '0.5px', fontSize: '9px' }}>
            {type}
          </Text>
          <Text size="sm" fw={600} style={{ whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
            {data.label || 'New Node'}
          </Text>
        </Box>
        {sample && (
          <Tooltip label="Latest processed sample">
            <ActionIcon 
              size="xs" 
              variant="subtle" 
              color="blue" 
              onClick={(e) => {
                e.stopPropagation();
                setSampleNodeId(id);
                setSampleInspectorOpened(true);
              }}
            >
              <IconEye size="1rem" />
            </ActionIcon>
          </Tooltip>
        )}
      </Box>

      {metric !== undefined && metric > 0 && (
        <Box style={{ position: 'absolute', bottom: -8, right: 10, background: 'var(--mantine-color-blue-6)', color: 'white', borderRadius: '10px', padding: '0 6px', fontSize: '9px', fontWeight: 700, zIndex: 10 }}>
          {metric.toLocaleString()}
        </Box>
      )}

      {errorCount !== undefined && errorCount > 0 && (
        <Box style={{ position: 'absolute', bottom: -8, right: metric > 0 ? 50 : 10, background: 'var(--mantine-color-red-6)', color: 'white', borderRadius: '10px', padding: '0 6px', fontSize: '9px', fontWeight: 700, zIndex: 10 }}>
          {errorCount.toLocaleString()} ERR
        </Box>
      )}

      {(data.dlqCount !== undefined || (data.isDLQ && workflowDeadLetterCount > 0)) && (
        <Box style={{ position: 'absolute', bottom: -8, left: 10, background: 'var(--mantine-color-orange-6)', color: 'white', borderRadius: '10px', padding: '0 6px', fontSize: '9px', fontWeight: 700 }}>
          ⚠️ {(data.dlqCount || (data.isDLQ ? workflowDeadLetterCount : 0)).toLocaleString()} FAILED
        </Box>
      )}

      {children}

      {data.testResult && (
        <Box 
          style={{ 
            position: 'absolute', 
            top: -10, 
            right: -10,
            background: data.testResult.status === 'COMPLETED' ? 'var(--mantine-color-green-6)' : 
                       data.testResult.status === 'ERROR' ? 'var(--mantine-color-red-6)' : 'var(--mantine-color-gray-6)',
            borderRadius: '50%',
            width: 20,
            height: 20,
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            color: 'white',
            fontSize: '10px',
            fontWeight: 'bold',
            boxShadow: '0 2px 4px rgba(0,0,0,0.2)'
          }}
        >
          {data.testResult.status === 'COMPLETED' ? '✓' : '!'}
        </Box>
      )}
    </Box>
  );
};
