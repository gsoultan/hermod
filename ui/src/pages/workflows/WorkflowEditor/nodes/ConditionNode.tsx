import { memo } from 'react';
import { Position } from '@xyflow/react';
import { Text } from '@mantine/core';
import { BaseNode, PlusHandle, TargetHandle } from './BaseNode';

import { IconArrowsSplit } from '@tabler/icons-react';
const ConditionNodeImpl = ({ id, data, selected }: any) => {
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

/*
 * Node components are memoised.
 *
 * React Flow re-renders the node layer whenever anything in it changes; without
 * memo, every node on the canvas re-rendered whenever any one node's telemetry
 * arrived — several times a second on a running workflow, each render rebuilding
 * a Paper/Group/Stack/ThemeIcon/Badge tree and its handles. React Flow's own
 * documentation requires this.
 */
export const ConditionNode = memo(ConditionNodeImpl);
ConditionNode.displayName = 'ConditionNode';
