import { memo } from 'react';
import { Position } from '@xyflow/react';
import { Badge } from '@mantine/core';
import { IconAlertTriangle, IconHandStop, IconRobot } from '@tabler/icons-react';
import {
  isWriteTool,
  needsApproval,
  toolList,
} from '@/components/workflow/Transformation/configs/ai-agent/agentTools';
import { BaseNode, PlusHandle, TargetHandle } from './BaseNode';

/**
 * The ai_agent node. It has one output: the agent returns no branch, and a
 * record whose run waited for approval leaves on the same output once it is
 * resumed. What the canvas must make obvious is that such a node can pause a
 * record for a person, or can write with nobody looking.
 */
const AIAgentNodeImpl = ({ id, data, selected }: any) => {
  const { tools } = toolList(data.tools);
  const pauses = tools.some(needsApproval);
  const unreviewed = tools.some((t) => isWriteTool(t) && !needsApproval(t));

  const badge = (
    <>
      <Badge size="xs" variant="outline" color="grape">
        {tools.length === 1 ? '1 tool' : `${tools.length} tools`}
      </Badge>
      {pauses && (
        <Badge size="xs" variant="light" color="yellow" leftSection={<IconHandStop size={10} />}>
          Waits for approval
        </Badge>
      )}
      {unreviewed && (
        <Badge size="xs" variant="light" color="red" leftSection={<IconAlertTriangle size={10} />}>
          Writes unreviewed
        </Badge>
      )}
    </>
  );

  return (
    <BaseNode id={id} type="AI Agent" color="grape" icon={IconRobot} data={data} selected={selected} badge={badge}>
      <TargetHandle position={Position.Left} color="grape" />
      <PlusHandle type="source" position={Position.Right} nodeId={id} color="grape" />
    </BaseNode>
  );
};

export const AIAgentNode = memo(AIAgentNodeImpl);
AIAgentNode.displayName = 'AIAgentNode';
