import { memo } from 'react';
import { Position } from '@xyflow/react';
import { Text } from '@mantine/core';
import { IconTags } from '@tabler/icons-react';
import { BaseNode, BranchOutputs, TargetHandle } from './BaseNode';
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
      {labelCount === 0 ? (
        <Text size="xs" c="dimmed" px={14} pb={8}>
          No labels yet
        </Text>
      ) : null}
      <BranchOutputs
        nodeId={id}
        branches={branches.map((branch) => ({
          id: branch,
          label: branch,
          color: branch === UNSURE_BRANCH ? 'gray' : 'grape',
        }))}
      />
    </BaseNode>
  );
};

export const AIClassifyNode = memo(AIClassifyNodeImpl);
AIClassifyNode.displayName = 'AIClassifyNode';
