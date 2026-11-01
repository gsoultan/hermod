import { memo } from 'react';
import { Position } from '@xyflow/react';
import { Text } from '@mantine/core';
import { IconBoxMultiple, IconLogin, IconLogout } from '@tabler/icons-react';
import { BaseNode, BranchOutputs, PlusHandle, TargetHandle, type Branch } from './BaseNode';
import { versionLabel } from '@/lib/subflow';

/**
 * A Run sub-flow step: one way out per Return step of the version it pins,
 * plus `error` for a failure the sub-flow does not handle itself. The return
 * names are copied onto the step when its sub-flow version is picked, so the
 * canvas can draw them without reading the sub-flow.
 */
const SubflowCallNodeImpl = ({ id, data, selected }: any) => {
  const returns: string[] = Array.isArray(data.returns) ? data.returns : [];
  const named = returns.filter((r) => r !== '');
  const branches: Branch[] = named.length > 1
    ? named.map((r) => ({ id: r, label: r, color: 'violet' }))
    : [{ id: null, label: named[0] || 'Result', color: 'violet' }];
  branches.push({ id: 'error', label: 'error', color: 'red' });
  return (
    <BaseNode
      id={id}
      type="Run sub-flow"
      color="violet"
      icon={IconBoxMultiple}
      data={data}
      selected={selected}
      badge={data.subflow_id ? (
        <Text size="xs" c="dimmed">{data.subflow_name || 'Sub-flow'} · {versionLabel(data.version)}</Text>
      ) : undefined}
    >
      <TargetHandle position={Position.Left} color="violet" />
      <BranchOutputs nodeId={id} branches={branches} />
    </BaseNode>
  );
};

/** Where a sub-flow starts: the data its caller maps to it comes out here. */
const SubflowInputNodeImpl = ({ id, data, selected }: any) => (
  <BaseNode id={id} type="Sub-flow input" color="violet" icon={IconLogin} data={data} selected={selected}>
    <PlusHandle type="source" position={Position.Right} nodeId={id} color="violet" />
  </BaseNode>
);

/** Where a sub-flow ends: what reaches it goes back to the step that called it. */
const SubflowReturnNodeImpl = ({ id, data, selected }: any) => (
  <BaseNode
    id={id}
    type="Return"
    color="violet"
    icon={IconLogout}
    data={data}
    selected={selected}
    badge={data.name ? <Text size="xs" c="dimmed">as “{data.name}”</Text> : undefined}
  >
    <TargetHandle position={Position.Left} color="violet" />
  </BaseNode>
);

export const SubflowCallNode = memo(SubflowCallNodeImpl);
export const SubflowInputNode = memo(SubflowInputNodeImpl);
export const SubflowReturnNode = memo(SubflowReturnNodeImpl);
SubflowCallNode.displayName = 'SubflowCallNode';
SubflowInputNode.displayName = 'SubflowInputNode';
SubflowReturnNode.displayName = 'SubflowReturnNode';
