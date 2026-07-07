import { useMemo } from 'react';
import { type Node, type Edge } from '@xyflow/react';
import { useWorkflowStore } from '../store/useWorkflowStore';
import { useShallow } from 'zustand/react/shallow';
import { getAllFieldsWithTypes, deepMergeSim, preparePayload, type FieldInfo } from '@/utils/transformationUtils';

export function useNodeContext(selectedNode: Node | null, testResults: any[] | null, sources: any[], sinks: any[]) {
  const { nodes, edges, nodeSamples } = useWorkflowStore(useShallow(state => ({
    nodes: state.nodes,
    edges: state.edges,
    nodeSamples: state.nodeSamples
  })));

  const contextDataRaw = useMemo(() => {
    let incomingPayload = null;
    let availableFields: FieldInfo[] = [];
    let sinkSchema = null;
    let upstreamSource = null;

    if (!selectedNode) return JSON.stringify({ incomingPayload, availableFields, sinkSchema, upstreamSource });

    // 1. Try to get payload from testResults (if simulation was run)
    if (testResults) {
      const incomingEdges = edges.filter((e: Edge) => e.target === selectedNode?.id);
      if (incomingEdges.length > 0) {
        const mergedPayload: Record<string, any> = {};
        incomingEdges.forEach((edge: Edge) => {
          const result = testResults!.find(r => r.node_id === edge.source);
          if (result && (result as any).payload) {
            deepMergeSim(mergedPayload, (result as any).payload);
          }
        });
        if (Object.keys(mergedPayload).length > 0) {
          incomingPayload = preparePayload(mergedPayload);
          availableFields = getAllFieldsWithTypes(incomingPayload);
        }
      }
    }

    // 2. Fallback: Use nearest upstream data from immediate predecessors only
    if (!incomingPayload) {
      const incomingEdges = edges.filter((e: Edge) => e.target === selectedNode.id);
      const mergedNearest: Record<string, any> = {};

      if (incomingEdges.length === 0) {
        const localTestPayload = (selectedNode.data?.testResult as any)?.payload;
        if (localTestPayload) {
          incomingPayload = preparePayload(localTestPayload);
          availableFields = getAllFieldsWithTypes(incomingPayload);
        } else if (selectedNode.data?.lastSample) {
          incomingPayload = preparePayload(selectedNode.data.lastSample);
          availableFields = getAllFieldsWithTypes(incomingPayload);
        } else if (nodeSamples?.[selectedNode.id]) {
          // Live sample captured from a running workflow (e.g. RabbitMQ source).
          incomingPayload = preparePayload(nodeSamples[selectedNode.id]);
          availableFields = getAllFieldsWithTypes(incomingPayload);
        } else if (selectedNode.type === 'source') {
          const sourceData = sources?.find((s: any) => s.id === selectedNode.data?.ref_id);
          const rawSample = sourceData?.sample;
          if (rawSample) {
            try {
              const sample = typeof rawSample === 'string' ? JSON.parse(rawSample) : rawSample;
              incomingPayload = preparePayload(sample);
              availableFields = getAllFieldsWithTypes(incomingPayload);
            } catch (e) {}
          }
        }
      } else {
        const visited = new Set<string>();
        const findNearestPayload = (nodeId: string): any | null => {
          if (visited.has(nodeId)) return null;
          visited.add(nodeId);
          const node = nodes.find(n => n.id === nodeId);
          if (!node) return null;

          const localTestPayload = (node.data?.testResult as any)?.payload;
          if (localTestPayload) return preparePayload(localTestPayload);
          const localLastSample = node.data?.lastSample;
          if (localLastSample) return preparePayload(localLastSample);
          const liveSample = nodeSamples?.[nodeId];
          if (liveSample) return preparePayload(liveSample);

          if (node.type === 'source') {
            const sourceData = sources?.find((s: any) => s.id === node.data?.ref_id);
            const rawSample = sourceData?.sample;
            if (rawSample) {
              try {
                const sample = typeof rawSample === 'string' ? JSON.parse(rawSample) : rawSample;
                return preparePayload(sample);
              } catch (e) {
                return null;
              }
            }
          }

          const inc = edges.filter((e: Edge) => e.target === nodeId);
          for (const e of inc) {
            const found = findNearestPayload(e.source);
            if (found) return found;
          }
          return null;
        };

        for (const edge of incomingEdges) {
          const payload = findNearestPayload(edge.source);
          if (payload) {
            deepMergeSim(mergedNearest, payload);
          }
        }

        if (Object.keys(mergedNearest).length > 0) {
          incomingPayload = preparePayload(mergedNearest);
          availableFields = getAllFieldsWithTypes(incomingPayload);
        }
      }
    }

    // 3. Try to get sink schema from downstream sink
    const downstreamEdges = edges.filter((e: Edge) => e.source === selectedNode?.id);
    if (downstreamEdges.length > 0) {
      const sinkNode = nodes.find(n => n.id === downstreamEdges[0].target);
      if (sinkNode && sinkNode.type === 'sink') {
        const sinkData = sinks?.find((s: any) => s.id === sinkNode.data.ref_id);
        if (sinkData && sinkData.config?.table) {
           sinkSchema = sinkData;
        }
      }
    }

    // 4. Try to find the nearest upstream source node
    const findNearestSource = (nodeId: string): any | null => {
      const node = nodes.find(n => n.id === nodeId);
      if (!node) return null;
      if (node.type === 'source') {
        return sources?.find((s: any) => s.id === node.data?.ref_id);
      }
      const inc = edges.filter((e: Edge) => e.target === nodeId);
      for (const e of inc) {
        const found = findNearestSource(e.source);
        if (found) return found;
      }
      return null;
    };

    const incomingEdgesForSource = edges.filter((e: Edge) => e.target === selectedNode.id);
    for (const edge of incomingEdgesForSource) {
      const src = findNearestSource(edge.source);
      if (src) {
        upstreamSource = src;
        break;
      }
    }

    // 5. Supplement availableFields with inferred fields from upstream transformations (Schema Propagation)
    // This ensures fields that WILL be added by upstream nodes are visible even without a sample/test.
    if (selectedNode.type !== 'source') {
      const isCDC = upstreamSource?.config?.use_cdc === 'true' || upstreamSource?.config?.use_cdc === true;
      const visitedTransformations = new Set<string>();

      const collectInferredFields = (nodeId: string) => {
        if (visitedTransformations.has(nodeId)) return;
        visitedTransformations.add(nodeId);

        const node = nodes.find(n => n.id === nodeId);
        if (!node) return;

        if (node.type === 'transformation') {
          const config = (node.data?.config || {}) as any;
          const targetField = config.targetField || config.target_field;
          if (targetField) {
            const path = isCDC ? `after.${targetField}` : targetField;
            if (!availableFields.find(f => f.path === path)) {
              availableFields.push({ path, type: 'any (inferred)' });
            }
          }
          // Also handle pipeline steps
          if (config.transType === 'pipeline' && config.steps) {
            let steps = [];
            try {
              steps = typeof config.steps === 'string' ? JSON.parse(config.steps) : config.steps;
            } catch (e) {}
            if (Array.isArray(steps)) {
              steps.forEach((s: any) => {
                if (s.targetField) {
                  const path = isCDC ? `after.${s.targetField}` : s.targetField;
                  if (!availableFields.find(f => f.path === path)) {
                    availableFields.push({ path, type: 'any (inferred)' });
                  }
                }
              });
            }
          }
        }

        const inc = edges.filter((e: Edge) => e.target === nodeId);
        inc.forEach(e => collectInferredFields(e.source));
      };

      const immediateIncoming = edges.filter((e: Edge) => e.target === selectedNode.id);
      immediateIncoming.forEach(e => collectInferredFields(e.source));
    }

    return JSON.stringify({ incomingPayload, availableFields, sinkSchema, upstreamSource });
  }, [selectedNode?.id, edges, nodes, testResults, sources, sinks, nodeSamples]);

  return useMemo(() => JSON.parse(contextDataRaw), [contextDataRaw]);
}
