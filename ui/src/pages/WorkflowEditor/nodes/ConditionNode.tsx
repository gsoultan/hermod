import { Position } from 'reactflow';
import { Text } from '@mantine/core';import { BaseNode, PlusHandle, TargetHandle } from './BaseNode';

import { IconArrowsSplit } from '@tabler/icons-react';
export const ConditionNode = ({ id, data, selected }: any) => {
  return (
    <BaseNode id={id} type="Condition" color="indigo" icon={IconArrowsSplit} data={data} selected={selected}>
      <TargetHandle position={Position.Left} color="indigo" />
      <PlusHandle type="source" position={Position.Right} id="true" nodeId={id} color="indigo" style={{ top: 30 }} />
      <PlusHandle type="source" position={Position.Right} id="false" nodeId={id} color="indigo" style={{ top: 55 }} />
      <Text size="xs" fw={700} color="indigo" style={{ position: 'absolute', right: 25, top: 22 }}>TRUE</Text>
      <Text size="xs" fw={700} color="indigo" style={{ position: 'absolute', right: 25, top: 47 }}>FALSE</Text>
    </BaseNode>
  );
};


