import { Position } from 'reactflow';
import { Text } from '@mantine/core';
import { BaseNode, PlusHandle, TargetHandle } from './BaseNode';
import { IconCircleCheck } from '@tabler/icons-react';

export const ApprovalNode = ({ id, data, selected }: any) => {
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
