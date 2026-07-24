import { Position, type Node as FlowNode } from '@xyflow/react';
import { 
  Text, Badge, Group, ThemeIcon, Paper, useMantineColorScheme, ActionIcon 
} from '@mantine/core';
import { BaseNode, PlusHandle, TargetHandle } from './BaseNode';
import { useState } from 'react';
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore';
import { IconArrowsSplit, IconChecklist, IconClock, IconCloud, IconCode, IconCopy, IconDatabase, IconEye, IconFilter, IconGitBranch, IconGitMerge, IconList, IconNote, IconPlaylist, IconSearch, IconShieldLock, IconTerminal2, IconTrash, IconVariable } from '@tabler/icons-react';
export const ValidatorNode = ({ id, data, selected }: any) => {
  return (
    <BaseNode id={id} type="Validator" color="orange" icon={IconChecklist} data={data} selected={selected}>
      <TargetHandle position={Position.Left} color="orange" />
      <PlusHandle type="source" position={Position.Right} nodeId={id} color="orange" />
    </BaseNode>
  );
};

export const TransformationNode = ({ id, data, selected }: any) => {
  const getIcon = () => {
    switch (data.transType) {
      case 'set': return IconVariable;
      case 'filter_data': return IconEye;
      case 'mask': return IconShieldLock;
      case 'db_lookup': return IconSearch;
      case 'api_lookup': return IconCloud;
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
      default: return IconFilter;
    }
  };

  const getLabel = () => {
    switch (data.transType) {
      case 'mapping': return 'Mapping';
      case 'set': return 'Set Fields';
      case 'filter_data': return 'Filter';
      case 'mask': return 'Mask';
      case 'db_lookup': return 'DB Lookup';
      case 'api_lookup': return 'API Lookup';
      case 'fuzzy_lookup': return 'Fuzzy Lookup';
      case 'char_map': return 'Char Map';
      case 'data_conversion': return 'Data Conversion';
      case 'execute_sql': return 'Execute SQL';
      case 'parallel_pipeline': return 'Parallel Pipeline';
      case 'pipeline': return 'Pipeline';
      case 'advanced': return 'Advanced';
      case 'lua': return 'Lua Script';
      case 'aggregate': return 'Aggregate';
      case 'sampling': return 'Sampling';
      case 'foreach': return 'Foreach';
      case 'fanout': return 'Fanout';
      case 'validator': return 'Validator';
      case 'stat_validator': return 'Statistical Validation';
      case 'dq_scorer': return 'Data Quality Scorer';
      default: return 'Transformation';
    }
  };

  return (
    <BaseNode id={id} type={getLabel()} color="violet" icon={getIcon()} data={data} selected={selected}>
      <TargetHandle position={Position.Left} color="violet" />
      <PlusHandle type="source" position={Position.Right} nodeId={id} color="violet" />
    </BaseNode>
  );
};

export const SwitchNode = ({ id, data, selected }: any) => {
  let cases: any[] = [];
  try {
    cases = typeof data.cases === 'string' ? JSON.parse(data.cases || '[]') : (data.cases || []);
  } catch(e) {}

  return (
    <BaseNode id={id} type="Switch" color="orange" icon={IconGitBranch} data={data} selected={selected}>
      <TargetHandle position={Position.Left} color="orange" />
      {cases.map((c: any, idx: number) => (
        <PlusHandle 
          key={idx}
          type="source" 
          position={Position.Right} 
          id={c.label || `case_${idx}`}
          nodeId={id} 
          color="orange"
          style={{ top: 30 + (idx * 25) }}
        />
      ))}
      {cases.length > 0 && (
        <Group gap={4} mt="xs">
          {cases.map((c: any, i: number) => (
            <Badge key={i} size="xs" variant="outline" color="orange">{c.label}</Badge>
          ))}
        </Group>
      )}
      <PlusHandle 
          type="source" 
          position={Position.Right} 
          id="default"
          nodeId={id} 
          color="gray"
          style={{ top: 30 + (cases.length * 25) }}
        />
        <Badge size="xs" variant="outline" color="gray" mt={4}>default</Badge>
    </BaseNode>
  );
};

export const RouterNode = ({ id, data, selected }: any) => {
  let rules: any[] = [];
  try {
    rules = typeof data.rules === 'string' ? JSON.parse(data.rules || '[]') : (data.rules || []);
  } catch(e) {}

  return (
    <BaseNode id={id} type="Router" color="indigo" icon={IconArrowsSplit} data={data} selected={selected}>
      <TargetHandle position={Position.Left} color="indigo" />
      {rules.map((rule: any, idx: number) => (
        <PlusHandle 
          key={idx}
          type="source" 
          position={Position.Right} 
          id={rule.label || `rule_${idx}`}
          nodeId={id} 
          color="indigo"
          style={{ top: 30 + (idx * 25) }}
        />
      ))}
      <PlusHandle 
        type="source" 
        position={Position.Right} 
        id="default"
        nodeId={id} 
        color="gray"
        style={{ top: 30 + (rules.length * 25) }}
      />
      {rules.length > 0 && (
        <Group gap={4} mt="xs">
          {rules.map((r: any, i: number) => (
            <Badge key={i} size="xs" variant="outline" color="indigo">{r.label}</Badge>
          ))}
          <Badge size="xs" variant="outline" color="gray">default</Badge>
        </Group>
      )}
    </BaseNode>
  );
};

export const MergeNode = ({ id, data, selected }: any) => {
  return (
    <BaseNode id={id} type="Merge" color="cyan" icon={IconGitMerge} data={data} selected={selected}>
      <TargetHandle position={Position.Left} color="cyan" />
      <PlusHandle type="source" position={Position.Right} nodeId={id} color="cyan" />
    </BaseNode>
  );
};

export const StatefulNode = ({ id, data, selected }: any) => {
  return (
    <BaseNode id={id} type="Stateful" color="pink" icon={IconDatabase} data={data} selected={selected}>
      <TargetHandle position={Position.Left} color="pink" />
      <PlusHandle type="source" position={Position.Right} nodeId={id} color="pink" />
    </BaseNode>
  );
};

export const WaitNode = ({ id, data, selected }: any) => {
  return (
    <BaseNode id={id} type="Wait" color="blue" icon={IconClock} data={data} selected={selected}>
      <TargetHandle position={Position.Left} color="blue" />
      <PlusHandle type="source" position={Position.Right} nodeId={id} color="blue" />
    </BaseNode>
  );
};

export const ForeachNode = ({ id, data, selected }: any) => {
  return (
    <BaseNode id={id} type="Foreach" color="teal" icon={IconPlaylist} data={data} selected={selected}>
      <TargetHandle position={Position.Left} color="teal" />
      <PlusHandle type="source" position={Position.Right} nodeId={id} color="teal" />
      <PlusHandle type="source" position={Position.Right} id="error" nodeId={id} color="red" style={{ top: 'auto', bottom: 10 }} />
    </BaseNode>
  );
};

export const LogNode = ({ id, data, selected }: any) => {
  return (
    <BaseNode id={id} type="Log" color="gray" icon={IconTerminal2} data={data} selected={selected}>
      <TargetHandle position={Position.Left} color="gray" />
      <PlusHandle type="source" position={Position.Right} nodeId={id} color="gray" />
    </BaseNode>
  );
};

export const CollectNode = ({ id, data, selected }: any) => {
  return (
    <BaseNode id={id} type="Collect" color="indigo" icon={IconList} data={data} selected={selected}>
      <TargetHandle position={Position.Left} color="indigo" />
      <PlusHandle type="source" position={Position.Right} nodeId={id} color="indigo" />
    </BaseNode>
  );
};

export const DeduplicateNode = ({ id, data, selected }: any) => {
  return (
    <BaseNode id={id} type="Deduplicate" color="violet" icon={IconCopy} data={data} selected={selected}>
      <TargetHandle position={Position.Left} color="violet" />
      <PlusHandle type="source" position={Position.Right} nodeId={id} color="violet" />
      <PlusHandle type="source" position={Position.Right} id="duplicate" nodeId={id} color="gray" style={{ top: 'auto', bottom: 10 }} />
    </BaseNode>
  );
};

export const NoteNode = ({ id, data, selected }: any) => {
  const { colorScheme } = useMantineColorScheme();
  const isDark = colorScheme === 'dark';
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
        <ActionIcon 
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
      <Paper
        p="sm"
        radius="md"
        style={{
          background: isDark ? 'var(--mantine-color-yellow-9)' : 'var(--mantine-color-yellow-1)',
          border: selected ? '2px solid var(--mantine-color-blue-6)' : '1px dashed var(--mantine-color-yellow-6)',
          minWidth: '150px',
          maxWidth: '250px',
          boxShadow: selected ? '0 0 10px rgba(0,0,0,0.1)' : 'none',
          transition: 'all 0.2s ease',
        }}
      >
        <Group gap="xs" mb={4}>
          <ThemeIcon variant="subtle" color="yellow" size="sm">
            <IconNote size="0.8rem" />
          </ThemeIcon>
          <Text size="xs" fw={700}>NOTE</Text>
        </Group>
        <Text size="sm">{data.label || 'Empty note...'}</Text>
      </Paper>
    </div>
  );
};


