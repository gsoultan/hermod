import { memo } from 'react';
import { getBezierPath } from 'reactflow';

// Polished connection line shown while dragging from a handle to create a new edge
export const ConnectionLine = memo((props: any) => {
  const { fromX, fromY, toX, toY, fromPosition, toPosition, connectionStatus } = props as any;

  const [path] = getBezierPath({
    sourceX: fromX,
    sourceY: fromY,
    targetX: toX,
    targetY: toY,
    sourcePosition: fromPosition,
    targetPosition: toPosition,
  });

  const isValid = connectionStatus !== 'invalid';
  const color = isValid ? 'var(--mantine-color-blue-6)' : 'var(--mantine-color-red-6)';

  return (
    <g>
      {/* subtle glow/backdrop to improve contrast on busy canvases */}
      <path
        d={path}
        fill="none"
        stroke={isValid ? 'rgba(59,130,246,0.25)' : 'rgba(239,68,68,0.25)'}
        strokeWidth={10}
        strokeLinecap="round"
      />
      {/* main line */}
      <path
        d={path}
        fill="none"
        stroke={color}
        strokeWidth={3}
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </g>
  );
});

ConnectionLine.displayName = 'ConnectionLine';
