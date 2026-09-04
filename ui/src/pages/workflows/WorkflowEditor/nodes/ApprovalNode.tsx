import { memo } from 'react';
import { Position } from '@xyflow/react';
import { Text } from '@mantine/core';
import { BaseNode, PlusHandle, TargetHandle } from './BaseNode';
import { IconCircleCheck } from '@tabler/icons-react';

const ApprovalNodeImpl = ({ id, data, selected }: any) => {
  return (
    <BaseNode id={id} type="Approval" color="green" icon={IconCircleCheck} data={data} selected={selected}>
      <TargetHandle position={Position.Left} color="green" />
      <PlusHandle type="source" position={Position.Right} id="approved" nodeId={id} color="green" style={{ top: 30 }} />
      <PlusHandle type="source" position={Position.Right} id="rejected" nodeId={id} color="red" style={{ top: 55 }} />
      <Text size="xs" fw={700} c="green" style={{ position: 'absolute', right: 25, top: 22 }}>APPROVED</Text>
      <Text size="xs" fw={700} c="red" style={{ position: 'absolute', right: 25, top: 47 }}>REJECTED</Text>
    </BaseNode>
  );
};

/*
 * Node components are memoised.
 *
 * React Flow re-renders the node layer whenever anything in it changes; without
 * memo, every node on the canvas re-rendered whenever any one node's telemetry
 * arrived — several times a second on a running workflow, each render rebuilding
 * a Paper/Group/Stack/ThemeIcon/Badge tree and its handles. React Flow's own
 * documentation requires this.
 */
export const ApprovalNode = memo(ApprovalNodeImpl);
ApprovalNode.displayName = 'ApprovalNode';
