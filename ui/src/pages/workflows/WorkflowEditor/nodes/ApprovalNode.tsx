import { memo } from 'react';
import { Position } from '@xyflow/react';
import { BaseNode, BranchOutputs, TargetHandle } from './BaseNode';
import { IconCircleCheck } from '@tabler/icons-react';

const ApprovalNodeImpl = ({ id, data, selected }: any) => {
  return (
    <BaseNode id={id} type="Approval" color="green" icon={IconCircleCheck} data={data} selected={selected}>
      <TargetHandle position={Position.Left} color="green" />
      <BranchOutputs nodeId={id} branches={[
        { id: 'approved', label: 'Approved', color: 'green' },
        { id: 'rejected', label: 'Rejected', color: 'red' },
      ]} />
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
