import { createContext, useContext, type ReactNode } from 'react';
import { Handle, Position } from 'reactflow';
import { Box, Text, useMantineColorScheme } from '@mantine/core';
import { IconPlus } from '@tabler/icons-react';

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

export const BaseNode = ({ type, color, icon: Icon, children, data }: { 
  id: string, 
  type: string, 
  color: string, 
  icon: any, 
  children: ReactNode, 
  data: any 
}) => {
  const { colorScheme } = useMantineColorScheme();
  const isDark = colorScheme === 'dark';

  return (
    <Box
      style={{
        background: isDark ? 'var(--mantine-color-dark-6)' : 'white',
        border: `2px solid ${data.isDLQ ? 'var(--mantine-color-orange-6)' : `var(--mantine-color-${color}-6)`}`,
        borderStyle: data.isDLQ ? 'dashed' : 'solid',
        borderRadius: '8px',
        padding: '12px',
        minWidth: '180px',
        boxShadow: '0 4px 6px rgba(0,0,0,0.1)',
        position: 'relative',
        opacity: data.testResult && data.testResult.status === 'FILTERED' ? 0.5 : 1,
      }}
    >
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
      </Box>

      {data.metric !== undefined && data.metric > 0 && (
        <Box style={{ position: 'absolute', bottom: -8, right: 10, background: 'var(--mantine-color-blue-6)', color: 'white', borderRadius: '10px', padding: '0 6px', fontSize: '9px', fontWeight: 700 }}>
          {data.metric.toLocaleString()}
        </Box>
      )}

      {data.dlqCount !== undefined && data.dlqCount > 0 && (
        <Box style={{ position: 'absolute', bottom: -8, left: 10, background: 'var(--mantine-color-orange-6)', color: 'white', borderRadius: '10px', padding: '0 6px', fontSize: '9px', fontWeight: 700 }}>
          ⚠️ {data.dlqCount.toLocaleString()} FAILED
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
