import { useMemo } from 'react';
import { type Node, type Edge } from '@xyflow/react';
import { useWorkflowStore } from '../store/useWorkflowStore';
import { useShallow } from 'zustand/react/shallow';
import { getAllFieldsWithTypes, deepMergeSim, preparePayload, getValByPath, type FieldInfo } from '@/utils/transformationUtils';
import { ownPayloadOf } from '../sampleCapture';

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

    // See ownPayloadOf for the order and why `lastSample` comes last.
    const ownPayload = (node: Node) => ownPayloadOf(node, sources, nodeSamples);

    // What the last preview says a node emitted. A node can have two entries —
    // an error and a "filtered" one when it fails — so take the one carrying a
    // payload rather than the first.
    const simulatedOutput = (nodeId: string): any | null =>
      (testResults?.find((r: any) => r.node_id === nodeId && r.payload) as any)?.payload ?? null;

    // 1. Try to get payload from testResults (if simulation was run)
    if (testResults) {
      const incomingEdges = edges.filter((e: Edge) => e.target === selectedNode?.id);
      if (incomingEdges.length > 0) {
        const mergedPayload: Record<string, any> = {};
        incomingEdges.forEach((edge: Edge) => {
          const output = simulatedOutput(edge.source);
          if (output) {
            deepMergeSim(mergedPayload, output);
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
        const own = ownPayload(selectedNode);
        if (own) {
          incomingPayload = preparePayload(own);
          availableFields = getAllFieldsWithTypes(incomingPayload);
        }
      } else {
        const visited = new Set<string>();
        const findNearestPayload = (nodeId: string): any | null => {
          if (visited.has(nodeId)) return null;
          visited.add(nodeId);
          const node = nodes.find(n => n.id === nodeId);
          if (!node) return null;

          // An upstream node's preview output before anything it holds itself:
          // when the node right before this one failed or filtered the sample,
          // the walk continues from the nearest output the preview did produce,
          // instead of dropping back to the source and losing every change the
          // nodes in between made.
          const simulated = simulatedOutput(nodeId);
          if (simulated) return preparePayload(simulated);
          const own = ownPayload(node);
          if (own) return preparePayload(own);

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

      // useWorkflowInitialization hydrates a saved node as
      // `data: { ...node.config, ref_id }` — the config is spread *flat* onto
      // data, which is why TransformationForm passes `config: selectedNode.data`.
      // Reading node.data.config here found an empty object for every workflow
      // loaded from storage, so none of the inferred fields below ever appeared
      // on a saved workflow. The nested shape is kept as a fallback because a
      // node built in memory can still carry one.
      const configOf = (node: Node): any => ({
        ...((node.data?.config as Record<string, any>) || {}),
        ...((node.data as Record<string, any>) || {}),
      });

      const addInferred = (path: string, type = 'any (inferred)') => {
        if (path && !availableFields.find(f => f.path === path)) {
          availableFields.push({ path, type });
        }
      };

      // A fan-out node rewrites what the rest of the graph sees, and none of it
      // is a targetField, so without this everything downstream of a foreach
      // showed the source's columns and no way to address the item.
      //
      // These paths are not `after.`-prefixed the way a transformation's
      // targetField is: the engine writes them with SetData, and
      // evaluator.GetMsgValByPath reads the data map before any CDC envelope, so
      // `_item` is what resolves at runtime on a CDC message too.
      const addFanoutNodeFields = (config: any) => {
        addInferred('_index', 'number (inferred)');
        addInferred('_item');
        // The element shape is the only thing a downstream mapping can actually
        // address, and the upstream sample already carries it.
        const arrayPath = config.arrayPath || config.array_path;
        if (arrayPath && incomingPayload) {
          const arr = getValByPath(incomingPayload, String(arrayPath));
          const first = Array.isArray(arr) ? arr[0] : null;
          if (first && typeof first === 'object' && !Array.isArray(first)) {
            getAllFieldsWithTypes(first, '_item').forEach(f => addInferred(f.path, `${f.type} (inferred)`));
          }
        }
        // ForeachNode drops the array it iterated from every message it emits —
        // carrying it onto all N is what made the fan-out cost quadratic. Listing
        // it here would offer a path that resolves to nothing at run time.
        if (arrayPath && config.keepSourceArray !== true && config.keepSourceArray !== 'true') {
          const consumed = String(arrayPath);
          availableFields = availableFields.filter(
            f => f.path !== consumed && !f.path.startsWith(`${consumed}.`)
          );
        }
      };

      const collectInferredFields = (nodeId: string) => {
        if (visitedTransformations.has(nodeId)) return;
        visitedTransformations.add(nodeId);

        const node = nodes.find(n => n.id === nodeId);
        if (!node) return;

        // Execution-level fan-out: one message per array item, each carrying
        // _item and _index (internal/engine/registry/nodes/control/foreach.go).
        if (node.type === 'foreach') {
          addFanoutNodeFields(configOf(node));
        }

        // Fan-in: the collected batch and its size
        // (internal/engine/registry/nodes/control/collect.go).
        if (node.type === 'collect') {
          const config = configOf(node);
          addInferred(config.targetField || config.target_field || '_items', 'array (inferred)');
          addInferred('_count', 'number (inferred)');
        }

        if (node.type === 'transformation') {
          const config = configOf(node);
          // The foreach/fanout *transformation* is a different node from the
          // foreach *node type*: it materialises the expanded array on the same
          // message instead of splitting it (pkg/comm/transformer/logic/foreach.go).
          if (config.transType === 'foreach' || config.transType === 'fanout') {
            addInferred(config.resultField || config.result_field || '_fanout', 'array (inferred)');
          }
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
            } catch {}
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
