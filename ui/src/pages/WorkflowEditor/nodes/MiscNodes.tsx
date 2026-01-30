import { Position } from 'reactflow';
import { 
  Text, Badge, Group, ThemeIcon, Paper, useMantineColorScheme 
} from '@mantine/core';
import { 
  IconFilter, IconGitBranch, IconGitMerge, IconDatabase, IconNote,
  IconVariable, IconEye, IconShieldLock, IconSearch, IconCloud,
  IconPlaylist, IconCode, IconChecklist, IconArrowsSplit
} from '@tabler/icons-react';
import { BaseNode, PlusHandle, TargetHandle } from './BaseNode';

export const ValidatorNode = ({ id, data }: any) => {
  return (
    <BaseNode id={id} type="Validator" color="orange" icon={IconChecklist} data={data}>
      <TargetHandle position={Position.Left} color="orange" />
      <PlusHandle type="source" position={Position.Right} nodeId={id} color="orange" />
    </BaseNode>
  );
};

export const TransformationNode = ({ id, data }: any) => {
  const getIcon = () => {
    switch (data.transType) {
      case 'set': return IconVariable;
      case 'filter_data': return IconEye;
      case 'mask': return IconShieldLock;
      case 'db_lookup': return IconSearch;
      case 'api_lookup': return IconCloud;
      case 'pipeline': return IconPlaylist;
      case 'advanced': return IconCode;
      case 'lua': return IconCode;
      case 'aggregate': return IconDatabase;
      case 'validator': return IconChecklist;
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
      case 'pipeline': return 'Pipeline';
      case 'advanced': return 'Advanced';
      case 'lua': return 'Lua Script';
      case 'aggregate': return 'Aggregate';
      case 'validator': return 'Validator';
      default: return 'Transformation';
    }
  };

  return (
    <BaseNode id={id} type={getLabel()} color="violet" icon={getIcon()} data={data}>
      <TargetHandle position={Position.Left} color="violet" />
      <PlusHandle type="source" position={Position.Right} nodeId={id} color="violet" />
    </BaseNode>
  );
};

export const SwitchNode = ({ id, data }: any) => {
  let cases: any[] = [];
  try {
    cases = typeof data.cases === 'string' ? JSON.parse(data.cases || '[]') : (data.cases || []);
  } catch(e) {}

  return (
    <BaseNode id={id} type="Switch" color="orange" icon={IconGitBranch} data={data}>
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

export const RouterNode = ({ id, data }: any) => {
  let rules: any[] = [];
  try {
    rules = typeof data.rules === 'string' ? JSON.parse(data.rules || '[]') : (data.rules || []);
  } catch(e) {}

  return (
    <BaseNode id={id} type="Router" color="indigo" icon={IconArrowsSplit} data={data}>
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

export const MergeNode = ({ id, data }: any) => {
  return (
    <BaseNode id={id} type="Merge" color="cyan" icon={IconGitMerge} data={data}>
      <TargetHandle position={Position.Left} color="cyan" />
      <PlusHandle type="source" position={Position.Right} nodeId={id} color="cyan" />
    </BaseNode>
  );
};

export const StatefulNode = ({ id, data }: any) => {
  return (
    <BaseNode id={id} type="Stateful" color="pink" icon={IconDatabase} data={data}>
      <TargetHandle position={Position.Left} color="pink" />
      <PlusHandle type="source" position={Position.Right} nodeId={id} color="pink" />
    </BaseNode>
  );
};

export const NoteNode = ({ data }: any) => {
  const { colorScheme } = useMantineColorScheme();
  const isDark = colorScheme === 'dark';
  return (
    <Paper
      p="sm"
      radius="md"
      style={{
        background: isDark ? 'var(--mantine-color-yellow-9)' : 'var(--mantine-color-yellow-1)',
        border: '1px dashed var(--mantine-color-yellow-6)',
        minWidth: '150px',
        maxWidth: '250px'
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
  );
};
