import { memo } from 'react';
import { Position } from '@xyflow/react';
import { Badge, Group, Text } from '@mantine/core';
import { IconTags } from '@tabler/icons-react';
import { BaseNode, PlusHandle, TargetHandle } from './BaseNode';
import { classifyBranchIds, UNSURE_BRANCH } from './branchHandleId';

/**
 * The ai_classify node: one output per label, then `unsure`.
 *
 * Like the Switch node, each handle's id is the branch name the engine
 * returns (classifyBranchIds), so an edge drawn from a label's handle is the
 * edge a message with that label follows.
 */
const AIClassifyNodeImpl = ({ id, data, selected }: any) => {
  const branches = classifyBranchIds(data.labels);
  const labelCount = branches.length - 1;

  return (
    <BaseNode id={id} type="AI Classify" color="grape" icon={IconTags} data={data} selected={selected}>
      <TargetHandle position={Position.Left} color="grape" />
      {branches.map((branch, idx) => (
        <PlusHandle
          key={branch}
          type="source"
          position={Position.Right}
          id={branch}
          nodeId={id}
          color={branch === UNSURE_BRANCH ? 'gray' : 'grape'}
          style={{ top: 30 + idx * 25 }}
        />
      ))}
      {labelCount === 0 ? (
        <Text size="xs" c="dimmed" mt="xs">
          No labels yet
        </Text>
      ) : null}
      <Group gap={4} mt="xs">
        {branches.map((branch) => (
          <Badge key={branch} size="xs" variant="outline" color={branch === UNSURE_BRANCH ? 'gray' : 'grape'}>
            {branch}
          </Badge>
        ))}
      </Group>
    </BaseNode>
  );
};

export const AIClassifyNode = memo(AIClassifyNodeImpl);
AIClassifyNode.displayName = 'AIClassifyNode';
