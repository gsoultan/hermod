import { Modal, Box, Text, Group, ThemeIcon, Loader, Alert } from '@mantine/core';import { useQuery } from '@tanstack/react-query';
import { apiFetch } from '../api';
import ReactFlow, { Background, Controls, MiniMap, type Node, type Edge, MarkerType } from 'reactflow';
import 'reactflow/dist/style.css';
import { useMemo } from 'react';import { IconAlertCircle, IconBroadcast, IconDatabase, IconGitBranch, IconPlug } from '@tabler/icons-react';
interface LineageEdge {
  source_id: string;
  source_name: string;
  source_type: string;
  sink_id: string;
  sink_name: string;
  sink_type: string;
  workflow_id: string;
  workflow_name: string;
}

export function DataLineageModal({ opened, onClose }: { opened: boolean; onClose: () => void }) {
  const { data, isLoading, error } = useQuery<LineageEdge[]>({
    queryKey: ['lineage'],
    queryFn: async () => {
      const res = await apiFetch('/api/infra/lineage');
      return res.json();
    },
    enabled: opened,
  });

  const { nodes, edges } = useMemo(() => {
    if (!data || !Array.isArray(data)) return { nodes: [], edges: [] };

    const nodeMap = new Map<string, Node>();
    const edgeList: Edge[] = [];

    data.forEach((item) => {
      // Source Node
      if (!nodeMap.has(item.source_id)) {
        nodeMap.set(item.source_id, {
          id: item.source_id,
          type: 'default',
          data: { label: (
            <Group gap="xs">
              <IconDatabase size="1rem" color="var(--mantine-color-blue-6)" />
              <Box>
                <Text size="xs" fw={700}>{item.source_name}</Text>
                <Text size="10px" c="dimmed">{item.source_type}</Text>
              </Box>
            </Group>
          ) },
          position: { x: 0, y: 0 }, // Will be laid out later
          style: { border: '2px solid var(--mantine-color-blue-6)', borderRadius: '8px', padding: '10px', width: 180 },
        });
      }

      // Workflow Node
      if (!nodeMap.has(item.workflow_id)) {
        nodeMap.set(item.workflow_id, {
          id: item.workflow_id,
          type: 'default',
          data: { label: (
            <Group gap="xs">
              <IconPlug size="1rem" color="var(--mantine-color-violet-6)" />
              <Box>
                <Text size="xs" fw={700}>{item.workflow_name}</Text>
                <Text size="10px" c="dimmed">Workflow</Text>
              </Box>
            </Group>
          ) },
          position: { x: 0, y: 0 },
          style: { border: '2px solid var(--mantine-color-violet-6)', borderRadius: '8px', padding: '10px', width: 180 },
        });
      }

      // Sink Node
      if (!nodeMap.has(item.sink_id)) {
        nodeMap.set(item.sink_id, {
          id: item.sink_id,
          type: 'default',
          data: { label: (
            <Group gap="xs">
              <IconBroadcast size="1rem" color="var(--mantine-color-green-6)" />
              <Box>
                <Text size="xs" fw={700}>{item.sink_name}</Text>
                <Text size="10px" c="dimmed">{item.sink_type}</Text>
              </Box>
            </Group>
          ) },
          position: { x: 0, y: 0 },
          style: { border: '2px solid var(--mantine-color-green-6)', borderRadius: '8px', padding: '10px', width: 180 },
        });
      }

      // Edges: Source -> Workflow
      const srcToWfId = `e-${item.source_id}-${item.workflow_id}`;
      if (!edgeList.find(e => e.id === srcToWfId)) {
        edgeList.push({
          id: srcToWfId,
          source: item.source_id,
          target: item.workflow_id,
          animated: true,
          markerEnd: { type: MarkerType.ArrowClosed, color: 'var(--mantine-color-gray-5)' },
          style: { stroke: 'var(--mantine-color-gray-5)', strokeWidth: 2 },
        });
      }

      // Edges: Workflow -> Sink
      const wfToSnkId = `e-${item.workflow_id}-${item.sink_id}`;
      if (!edgeList.find(e => e.id === wfToSnkId)) {
        edgeList.push({
          id: wfToSnkId,
          source: item.workflow_id,
          target: item.sink_id,
          animated: true,
          markerEnd: { type: MarkerType.ArrowClosed, color: 'var(--mantine-color-gray-5)' },
          style: { stroke: 'var(--mantine-color-gray-5)', strokeWidth: 2 },
        });
      }
    });

    // Simple layout logic: sources left, workflows middle, sinks right
    const nodesArray = Array.from(nodeMap.values());
    const sourceNodes = nodesArray.filter(n => (data || []).some(d => d.source_id === n.id && d.workflow_id !== n.id));
    const workflowNodes = nodesArray.filter(n => (data || []).some(d => d.workflow_id === n.id));
    const sinkNodes = nodesArray.filter(n => (data || []).some(d => d.sink_id === n.id && d.workflow_id !== n.id));

    sourceNodes.forEach((n, i) => { n.position = { x: 50, y: i * 100 + 50 }; });
    workflowNodes.forEach((n, i) => { n.position = { x: 350, y: i * 100 + 50 }; });
    sinkNodes.forEach((n, i) => { n.position = { x: 650, y: i * 100 + 50 }; });

    return { nodes: nodesArray, edges: edgeList };
  }, [data]);

  return (
    <Modal
      opened={opened}
      onClose={onClose}
      title={
        <Group gap="xs">
          <ThemeIcon variant="light" color="blue">
            <IconGitBranch size="1.2rem" />
          </ThemeIcon>
          <Text fw={700}>Global Data Lineage</Text>
        </Group>
      }
      size="90%"
      fullScreen
    >
      <Box h="calc(100vh - 150px)" style={{ position: 'relative', border: '1px solid var(--mantine-color-gray-3)', borderRadius: '8px' }}>
        {isLoading && (
          <Group justify="center" align="center" h="100%">
            <Loader size="lg" />
            <Text>Generating lineage map...</Text>
          </Group>
        )}
        {error && (
          <Alert icon={<IconAlertCircle size="1rem" />} color="red" m="md">
            Failed to load lineage data: {(error as any).message}
          </Alert>
        )}
        {!isLoading && !error && data && data.length === 0 && (
          <Group justify="center" align="center" h="100%">
            <Text c="dimmed">No active data pipelines found to generate lineage.</Text>
          </Group>
        )}
        {!isLoading && !error && data && data.length > 0 && (
          <ReactFlow
            nodes={nodes}
            edges={edges}
            fitView
            style={{ background: 'var(--mantine-color-gray-0)' }}
          >
            <Background color="#aaa" gap={20} />
            <Controls />
            <MiniMap />
          </ReactFlow>
        )}
      </Box>
    </Modal>
  );
}


