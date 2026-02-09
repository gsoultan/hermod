import { memo, useMemo, useState } from 'react';
import { BaseEdge, EdgeLabelRenderer, getBezierPath, type EdgeProps } from 'reactflow';
import { HoverCard, Code, Stack, Text, ActionIcon, Tooltip, Badge } from '@mantine/core';
import { useWorkflowStore } from '../store/useWorkflowStore';
import { IconTrash, IconPlayerPause, IconPlayerPlay } from '@tabler/icons-react';
function formatPreview(val: any): string {
  try {
    if (typeof val === 'string') return val.length > 200 ? val.slice(0, 200) + '…' : val;
    return JSON.stringify(val, null, 2).slice(0, 400);
  } catch {
    return String(val);
  }
}

export const LiveEdge = memo((props: EdgeProps) => {
  const { id, source, sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition, markerEnd, selected, data } = props;
  const [edgePath, labelX, labelY] = getBezierPath({ sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition });

  const [hovered, setHovered] = useState(false);

  const pulseEnabled = useWorkflowStore(s => s.pulseEnabled);
  const nodeMetrics = useWorkflowStore(s => s.nodeMetrics);
  const edgeThroughput = useWorkflowStore(s => s.edgeThroughput);
  const nodeSamples = useWorkflowStore(s => s.nodeSamples);
  const setEdges = useWorkflowStore(s => s.setEdges);
  const hasBreakpoint = !!(data as any)?.breakpoint;

  // Use per-edge throughput if available, else fall back to source node throughput
  const throughput = edgeThroughput[`${source}->${id.includes(':::') ? id.split(':::')[1] : props.target}`] ?? 
                    edgeThroughput[`${source}->${props.target}`] ?? 
                    nodeMetrics[source] ?? 0;
  const srcSample = nodeSamples[source];
  const samples = Array.isArray(srcSample) ? srcSample : (srcSample ? [srcSample] : []);

  const strokeWidth = useMemo(() => {
    if (!pulseEnabled) return 1.5;
    // Map throughput (msgs/s) → stroke width [1.5..3.5]
    const w = Math.max(1.5, Math.min(3.5, 1.5 + Math.log10(throughput + 1)));
    return parseFloat(w.toFixed(2));
  }, [throughput, pulseEnabled]);

  const dashAnim = useMemo(() => {
    if (!pulseEnabled) return undefined;
    // Speed up with throughput; base duration 2s → min 0.6s
    const dur = Math.max(0.6, 2 / Math.log2(throughput + 2));
    return `${dur}s linear infinite dash`;
  }, [throughput, pulseEnabled]);

  return (
    <>
      <style>
        {`
          /* Animate dash movement from left → right along the edge path */
          @keyframes dash { to { stroke-dashoffset: 1000; } }
          .react-flow__edge-path {
            transition: stroke-width 0.2s, stroke 0.2s;
          }
        `}
      </style>
      <BaseEdge
        id={id}
        path={edgePath}
        markerEnd={markerEnd}
        style={{
          stroke: hasBreakpoint
            ? (selected ? 'var(--mantine-color-orange-7)' : 'var(--mantine-color-orange-6)')
            : (selected ? 'var(--mantine-color-blue-7)' : (pulseEnabled ? 'var(--mantine-color-blue-6)' : 'var(--mantine-color-gray-6)')),
          strokeWidth: selected ? strokeWidth + 1.5 : strokeWidth,
          strokeDasharray: hasBreakpoint ? '2 6' : (pulseEnabled ? '8 6' : 'none'),
          animation: dashAnim ? `${dashAnim}` : undefined,
          cursor: 'pointer',
          strokeLinecap: 'round',
          strokeLinejoin: 'round'
        }}
      />
      <EdgeLabelRenderer>
        <div
          style={{ 
            position: 'absolute', 
            transform: `translate(-50%, -50%) translate(${labelX}px, ${labelY}px)`, 
            pointerEvents: 'all',
            display: 'flex',
            alignItems: 'center',
            gap: '6px'
          }}
          onMouseEnter={() => setHovered(true)}
          onMouseLeave={() => setHovered(false)}
        >
          <HoverCard withArrow shadow="md" position="top" offset={6} withinPortal>
            <HoverCard.Target>
              <div style={{ 
                width: 12, 
                height: 12, 
                borderRadius: 999, 
                background: hasBreakpoint
                  ? (selected ? 'var(--mantine-color-orange-7)' : 'var(--mantine-color-orange-5)')
                  : (selected ? 'var(--mantine-color-blue-7)' : 'var(--mantine-color-blue-5)'), 
                cursor: 'help', 
                opacity: 0.8,
                border: selected ? '2px solid white' : 'none',
                boxShadow: '0 0 4px rgba(0,0,0,0.2)'
              }} />
            </HoverCard.Target>
            <HoverCard.Dropdown>
              <Stack gap="xs" maw={360}>
                <Text fw={700} size="xs">Recent messages ({samples.length})</Text>
                {hasBreakpoint && <Badge size="xs" color="orange" variant="light">Breakpoint</Badge>}
                {samples.length === 0 ? (
                  <Text size="xs" c="dimmed">No samples yet</Text>
                ) : (
                  samples.slice(0, 5).map((s: any, i: number) => (
                    <Code key={i} block style={{ fontSize: 10 }}>
                      {formatPreview(s)}
                    </Code>
                  ))
                )}
                {pulseEnabled && (
                  <Text size="xs" c="dimmed">Throughput: {throughput.toFixed(2)} msg/s</Text>
                )}
              </Stack>
            </HoverCard.Dropdown>
          </HoverCard>

          {(selected || hovered) && (
            <div style={{ display: 'flex', gap: 6 }}>
              <Tooltip label={hasBreakpoint ? 'Remove breakpoint' : 'Add breakpoint'} position="right" withArrow>
                <ActionIcon 
                  color={hasBreakpoint ? 'orange' : 'gray'} 
                  variant={hasBreakpoint ? 'filled' : 'light'} 
                  size="sm" 
                  radius="xl"
                  onClick={(e) => {
                    e.stopPropagation();
                    setEdges((eds) => eds.map((edge) => edge.id === id ? {
                      ...edge,
                      data: { ...(edge.data || {}), breakpoint: !hasBreakpoint }
                    } : edge));
                  }}
                  style={{ boxShadow: '0 0 4px rgba(0,0,0,0.2)' }}
                >
                  {hasBreakpoint ? <IconPlayerPlay size="0.9rem" /> : <IconPlayerPause size="0.9rem" />}
                </ActionIcon>
              </Tooltip>
              <Tooltip label="Remove connection" position="right" withArrow>
                <ActionIcon 
                  color="red" 
                  variant="filled" 
                  size="sm" 
                  radius="xl"
                  onClick={(e) => {
                    e.stopPropagation();
                    setEdges((eds) => eds.filter((edge) => edge.id !== id));
                  }}
                  style={{ boxShadow: '0 0 4px rgba(0,0,0,0.2)' }}
                >
                  <IconTrash size="0.9rem" />
                </ActionIcon>
              </Tooltip>
            </div>
          )}
        </div>
      </EdgeLabelRenderer>
    </>
  );
});

LiveEdge.displayName = 'LiveEdge';


