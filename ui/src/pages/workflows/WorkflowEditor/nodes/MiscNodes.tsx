import { Position, type Node as FlowNode } from '@xyflow/react';
import { 
  Text, Group, Paper, ActionIcon, rem
} from '@mantine/core';
import { transformationLabel } from './transformationLabel';
import { BaseNode, BranchOutputs, PlusHandle, TargetHandle } from './BaseNode';
import { branchHandleId } from './branchHandleId';
import { useState, memo } from 'react';
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore';
import { IconAlertTriangle, IconArrowsMaximize, IconArrowsSplit, IconBinary, IconBolt, IconBraces, IconChartHistogram, IconChecklist, IconClock, IconCloud, IconCode, IconCopy, IconDatabase, IconDatabaseSearch, IconEye, IconFileSearch, IconFilter, IconGitBranch, IconGitCompare, IconGitMerge, IconLayoutList, IconList, IconLock, IconLockOpen, IconMail, IconMapPin, IconNote, IconPlaylist, IconScale, IconSearch, IconShieldLock, IconSparkles, IconStack2, IconTemplate, IconTerminal2, IconTextScan2, IconTimeline, IconTrash, IconVariable, IconVector } from '@tabler/icons-react';
const ValidatorNodeImpl = ({ id, data, selected }: any) => {
  return (
    <BaseNode id={id} type="Validator" color="orange" icon={IconChecklist} data={data} selected={selected}>
      <TargetHandle position={Position.Left} color="orange" />
      <PlusHandle type="source" position={Position.Right} nodeId={id} color="orange" />
    </BaseNode>
  );
};

const TransformationNodeImpl = ({ id, data, selected }: any) => {
  const getIcon = () => {
    switch (data.transType) {
      case 'set': return IconVariable;
      case 'filter_data': return IconEye;
      case 'mask': return IconShieldLock;
      case 'encrypt': return IconLock;
      case 'decrypt': return IconLockOpen;
      case 'db_lookup': return IconSearch;
      case 'api_lookup': return IconCloud;
      case 'redis_lookup': return IconBolt;
      case 'redis_set': return IconBolt;
      case 'panmail_providers': return IconMail;
      case 'fuzzy_lookup': return IconSearch;
      case 'char_map': return IconTerminal2;
      case 'data_conversion': return IconArrowsSplit;
      case 'execute_sql': return IconDatabase;
      case 'pipeline': return IconPlaylist;
      case 'advanced': return IconCode;
      case 'lua': return IconCode;
      case 'aggregate': return IconDatabase;
      case 'sampling': return IconFilter;
      case 'foreach': return IconList;
      case 'fanout': return IconArrowsSplit;
      case 'validator': return IconChecklist;
      case 'stat_validator': return IconChecklist;
      case 'dq_scorer': return IconChecklist;
      case 'ai_prompt': return IconSparkles;
      case 'ai_extract': return IconTextScan2;
      case 'ai_embed': return IconVector;
      case 'ai_retrieve': return IconDatabaseSearch;
      case 'flatten': return IconLayoutList;
      case 'unflatten': return IconStack2;
      case 'parse_field': return IconBraces;
      case 'template_render': return IconTemplate;
      case 'field_diff': return IconGitCompare;
      case 'reference_lookup': return IconFileSearch;
      case 'geo': return IconMapPin;
      case 'scale': return IconScale;
      case 'encode': return IconBinary;
      case 'bucketize': return IconChartHistogram;
      case 'rolling': return IconTimeline;
      case 'anomaly_score': return IconAlertTriangle;
      default: return IconFilter;
    }
  };

  const getLabel = () => transformationLabel(data.transType);

  return (
    <BaseNode id={id} type={getLabel()} color="violet" icon={getIcon()} data={data} selected={selected}>
      <TargetHandle position={Position.Left} color="violet" />
      <PlusHandle type="source" position={Position.Right} nodeId={id} color="violet" />
    </BaseNode>
  );
};

const SwitchNodeImpl = ({ id, data, selected }: any) => {
  let cases: any[] = [];
  try {
    cases = typeof data.cases === 'string' ? JSON.parse(data.cases || '[]') : (data.cases || []);
  } catch {}

  return (
    <BaseNode id={id} type="Switch" color="orange" icon={IconGitBranch} data={data} selected={selected}>
      <TargetHandle position={Position.Left} color="orange" />
      <BranchOutputs
        nodeId={id}
        branches={[
          ...cases.map((c: any, idx: number) => {
            const branch = branchHandleId('switch', c.label, idx);
            return { id: branch, label: branch, color: 'orange' };
          }),
          { id: 'default', label: 'default', color: 'gray' },
        ]}
      />
    </BaseNode>
  );
};

const RouterNodeImpl = ({ id, data, selected }: any) => {
  let rules: any[] = [];
  try {
    rules = typeof data.rules === 'string' ? JSON.parse(data.rules || '[]') : (data.rules || []);
  } catch {}

  return (
    <BaseNode id={id} type="Router" color="indigo" icon={IconArrowsSplit} data={data} selected={selected}>
      <TargetHandle position={Position.Left} color="indigo" />
      <BranchOutputs
        nodeId={id}
        branches={[
          ...rules.map((rule: any, idx: number) => {
            const branch = branchHandleId('router', rule.label, idx);
            return { id: branch, label: branch, color: 'indigo' };
          }),
          { id: 'default', label: 'default', color: 'gray' },
        ]}
      />
    </BaseNode>
  );
};

const MergeNodeImpl = ({ id, data, selected }: any) => {
  return (
    <BaseNode id={id} type="Merge" color="cyan" icon={IconGitMerge} data={data} selected={selected}>
      <TargetHandle position={Position.Left} color="cyan" />
      <PlusHandle type="source" position={Position.Right} nodeId={id} color="cyan" />
    </BaseNode>
  );
};

const StatefulNodeImpl = ({ id, data, selected }: any) => {
  return (
    <BaseNode id={id} type="Stateful" color="pink" icon={IconDatabase} data={data} selected={selected}>
      <TargetHandle position={Position.Left} color="pink" />
      <PlusHandle type="source" position={Position.Right} nodeId={id} color="pink" />
    </BaseNode>
  );
};

const WaitNodeImpl = ({ id, data, selected }: any) => {
  return (
    <BaseNode id={id} type="Wait" color="blue" icon={IconClock} data={data} selected={selected}>
      <TargetHandle position={Position.Left} color="blue" />
      <PlusHandle type="source" position={Position.Right} nodeId={id} color="blue" />
    </BaseNode>
  );
};

const ForeachNodeImpl = ({ id, data, selected }: any) => {
  return (
    <BaseNode id={id} type="Foreach" color="teal" icon={IconPlaylist} data={data} selected={selected}>
      <TargetHandle position={Position.Left} color="teal" />
      <BranchOutputs nodeId={id} branches={[
        { id: null, label: 'Each item', color: 'teal' },
        { id: 'error', label: 'Error', color: 'red' },
      ]} />
    </BaseNode>
  );
};

const ExplodeNodeImpl = ({ id, data, selected }: any) => {
  return (
    <BaseNode id={id} type="Explode" color="cyan" icon={IconArrowsMaximize} data={data} selected={selected}>
      <TargetHandle position={Position.Left} color="cyan" />
      <PlusHandle type="source" position={Position.Right} nodeId={id} color="cyan" />
    </BaseNode>
  );
};

const LogNodeImpl = ({ id, data, selected }: any) => {
  return (
    <BaseNode id={id} type="Log" color="gray" icon={IconTerminal2} data={data} selected={selected}>
      <TargetHandle position={Position.Left} color="gray" />
      <PlusHandle type="source" position={Position.Right} nodeId={id} color="gray" />
    </BaseNode>
  );
};

const CollectNodeImpl = ({ id, data, selected }: any) => {
  return (
    <BaseNode id={id} type="Collect" color="indigo" icon={IconList} data={data} selected={selected}>
      <TargetHandle position={Position.Left} color="indigo" />
      <PlusHandle type="source" position={Position.Right} nodeId={id} color="indigo" />
    </BaseNode>
  );
};

const DeduplicateNodeImpl = ({ id, data, selected }: any) => {
  return (
    <BaseNode id={id} type="Deduplicate" color="violet" icon={IconCopy} data={data} selected={selected}>
      <TargetHandle position={Position.Left} color="violet" />
      <BranchOutputs nodeId={id} branches={[
        { id: null, label: 'Unique', color: 'violet' },
        { id: 'duplicate', label: 'Duplicate', color: 'gray' },
      ]} />
    </BaseNode>
  );
};

const NoteNodeImpl = ({ id, data, selected }: any) => {
  const [hovered, setHovered] = useState(false);
  const setNodes = useWorkflowStore((s: any) => s.setNodes);
  const setSelectedNode = useWorkflowStore((s: any) => s.setSelectedNode);

  const onDelete = (e: React.MouseEvent) => {
    e.stopPropagation();
    setNodes((nds: FlowNode[]) => nds.filter((n: FlowNode) => n.id !== id));
    setSelectedNode(null);
  };

  return (
    <div 
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
      style={{ position: 'relative' }}
    >
      {(hovered || selected) && (
        <ActionIcon aria-label={`Delete note ${data.label ? `"${data.label}"` : ""}`.trim()} 
          variant="filled" 
          color="red" 
          size="xs" 
          radius="xl"
          style={{ position: 'absolute', top: -8, right: -8, zIndex: 110 }}
          onClick={onDelete}
        >
          <IconTrash size="0.7rem" />
        </ActionIcon>
      )}
      <Paper className="hermod-note" data-selected={selected || undefined} radius="md" px={14} py={12}>
        <Group gap={8} mb={6} wrap="nowrap">
          <IconNote size="0.9rem" color="var(--mantine-color-yellow-7)" />
          <Text size="xs" fw={600} c="dimmed" tt="uppercase" style={{ letterSpacing: rem(0.6) }}>Note</Text>
        </Group>
        {data.label
          ? <Text size="sm" style={{ whiteSpace: 'pre-wrap' }}>{data.label}</Text>
          : <Text size="sm" c="dimmed">Empty note</Text>}
      </Paper>
    </div>
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
export const ValidatorNode = memo(ValidatorNodeImpl);
export const TransformationNode = memo(TransformationNodeImpl);
export const SwitchNode = memo(SwitchNodeImpl);
export const RouterNode = memo(RouterNodeImpl);
export const MergeNode = memo(MergeNodeImpl);
export const StatefulNode = memo(StatefulNodeImpl);
export const WaitNode = memo(WaitNodeImpl);
export const ForeachNode = memo(ForeachNodeImpl);
export const ExplodeNode = memo(ExplodeNodeImpl);
export const LogNode = memo(LogNodeImpl);
export const CollectNode = memo(CollectNodeImpl);
export const DeduplicateNode = memo(DeduplicateNodeImpl);
export const NoteNode = memo(NoteNodeImpl);
ValidatorNode.displayName = 'ValidatorNode';
TransformationNode.displayName = 'TransformationNode';
SwitchNode.displayName = 'SwitchNode';
RouterNode.displayName = 'RouterNode';
MergeNode.displayName = 'MergeNode';
StatefulNode.displayName = 'StatefulNode';
WaitNode.displayName = 'WaitNode';
ForeachNode.displayName = 'ForeachNode';
ExplodeNode.displayName = 'ExplodeNode';
LogNode.displayName = 'LogNode';
CollectNode.displayName = 'CollectNode';
DeduplicateNode.displayName = 'DeduplicateNode';
NoteNode.displayName = 'NoteNode';
