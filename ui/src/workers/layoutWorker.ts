/// <reference lib="webworker" />
// Web worker to run Dagre layout off the main thread
import dagre from 'dagre';

export type LayoutRequest = {
  id: number;
  direction: 'LR' | 'TB' | 'RL' | 'BT';
  nodes: { id: string }[];
  edges: { source: string; target: string }[];
};

export type LayoutResponse = {
  id: number;
  nodes: { id: string; x: number; y: number }[];
};

// eslint-disable-next-line no-restricted-globals
self.onmessage = (e: MessageEvent<LayoutRequest>) => {
  const { id, direction, nodes, edges } = e.data;

  try {
    const g = new dagre.graphlib.Graph();
    g.setDefaultEdgeLabel(() => ({}));
    g.setGraph({ rankdir: direction });

    for (const n of nodes) {
      g.setNode(n.id, { width: 250, height: 100 });
    }
    for (const e of edges) {
      g.setEdge(e.source, e.target);
    }

    dagre.layout(g);

    const result: LayoutResponse = {
      id,
      nodes: nodes.map((n) => {
        const pos = g.node(n.id);
        return {
          id: n.id,
          x: (pos?.x ?? 0) - 125,
          y: (pos?.y ?? 0) - 50,
        };
      }),
    };

    // eslint-disable-next-line no-restricted-globals
    (self as unknown as Worker).postMessage(result);
  } catch (_err) {
    // On error, just return zero positions so caller can fallback
    // eslint-disable-next-line no-restricted-globals
    (self as unknown as Worker).postMessage({ id, nodes: nodes.map((n) => ({ id: n.id, x: 0, y: 0 })) } as LayoutResponse);
  }
};
