import { useCallback, useEffect, useRef } from 'react';
import { useReactFlow } from 'reactflow';
import { useWorkflowStore } from '../store/useWorkflowStore';

export function useWorkflowLayout() {
  const { getNodes, getEdges } = useReactFlow();
  const { setNodes, setEdges } = useWorkflowStore();
  const workerRef = useRef<Worker | null>(null);
  const seqRef = useRef(0);
  const latestSeqRef = useRef(0);

  const onLayout = useCallback((direction: 'LR' | 'TB' | 'RL' | 'BT' = 'LR') => {
    const nodes = getNodes();
    const edges = getEdges();

    try {
      if (!workerRef.current) {
        workerRef.current = new Worker(new URL('../../../workers/layoutWorker.ts', import.meta.url), { type: 'module' });
        // Attach error channels once on creation
        workerRef.current.onerror = () => {
          // Fallback: simple horizontal layout to avoid blocking
          const nowNodes = getNodes();
          const nowEdges = getEdges();
          const layoutedNodes = nowNodes.map((n, i) => ({
            ...n,
            position: { x: i * 280, y: 0 },
          }));
          setNodes(layoutedNodes);
          setEdges(nowEdges);
        };
        workerRef.current.onmessageerror = () => {
          const nowNodes = getNodes();
          const nowEdges = getEdges();
          const layoutedNodes = nowNodes.map((n, i) => ({
            ...n,
            position: { x: i * 280, y: 0 },
          }));
          setNodes(layoutedNodes);
          setEdges(nowEdges);
        };
      }
      const id = ++seqRef.current;
      latestSeqRef.current = id;
      workerRef.current.onmessage = (ev: MessageEvent<{ id: number; nodes: { id: string; x: number; y: number }[] }>) => {
        if (ev.data?.id !== latestSeqRef.current) return;
        const posMap = new Map(ev.data.nodes.map((n) => [n.id, { x: n.x, y: n.y }]));
        const layoutedNodes = nodes.map((node) => {
          const p = posMap.get(node.id);
          return p ? { ...node, position: { x: p.x, y: p.y } } : node;
        });
        setNodes(layoutedNodes);
        setEdges(edges);
      };
      workerRef.current.postMessage({
        id,
        direction,
        nodes: nodes.map((n) => ({ id: n.id })),
        edges: edges.map((e) => ({ source: e.source, target: e.target })),
      });
    } catch (_err) {
      // Fallback: simple horizontal layout to avoid blocking
      const layoutedNodes = nodes.map((n, i) => ({
        ...n,
        position: { x: i * 280, y: 0 },
      }));
      setNodes(layoutedNodes);
      setEdges(edges);
    }
  }, [getNodes, getEdges, setNodes, setEdges]);

  // Ensure worker is terminated on unmount to avoid leaks
  useEffect(() => {
    return () => {
      if (workerRef.current) {
        workerRef.current.onmessage = null;
        workerRef.current.onerror = null;
        workerRef.current.onmessageerror = null;
        workerRef.current.terminate();
        workerRef.current = null;
      }
    };
  }, []);

  return { onLayout };
}
