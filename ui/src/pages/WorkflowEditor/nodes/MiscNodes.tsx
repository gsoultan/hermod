import { Position } from 'reactflow';
import { 
  Text, Badge, Group, ThemeIcon, Paper, useMantineColorScheme 
} from '@mantine/core';
import { 
  IconFilter, IconGitBranch, IconGitMerge, IconDatabase, IconNote,
  IconVariable, IconEye, IconShieldLock, IconSearch, IconCloud,
  IconPlaylist, IconCode
} from '@tabler/icons-react';
import { BaseNode, PlusHandle, TargetHandle } from './BaseNode';

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
  const branches = data.branches || [];
  return (
    <BaseNode id={id} type="Switch" color="orange" icon={IconGitBranch} data={data}>
      <TargetHandle position={Position.Left} color="orange" />
      {branches.map((branch: any, idx: number) => (
        <PlusHandle 
          key={idx}
          type="source" 
          position={Position.Right} 
          id={branch.label || `branch_${idx}`}
          nodeId={id} 
          color="orange"
          style={{ top: 30 + (idx * 25) }}
        />
      ))}
      {branches.length > 0 && (
        <Group gap={4} mt="xs">
          {branches.map((b: any, i: number) => (
            <Badge key={i} size="xs" variant="outline" color="orange">{b.label}</Badge>
          ))}
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
