import dagre from 'dagre';

/** The size a step is laid out at when it has not been drawn yet. */
const DEFAULT_WIDTH = 250;
const DEFAULT_HEIGHT = 100;

export type LayoutRequest = {
  id: number;
  direction: 'LR' | 'TB' | 'RL' | 'BT';
  /** Each step at the size it was drawn, when known, so a tall step keeps its neighbours off it. */
  nodes: { id: string; width?: number; height?: number }[];
  edges: { source: string; target: string }[];
};

export type LayoutResponse = {
  id: number;
  /** Top-left corners, which is how React Flow positions a node. */
  nodes: { id: string; x: number; y: number }[];
};

/** Lays the steps out with dagre. Kept apart from the worker so it can be tested. */
export function layoutPositions({ id, direction, nodes, edges }: LayoutRequest): LayoutResponse {
  const g = new dagre.graphlib.Graph();
  g.setDefaultEdgeLabel(() => ({}));
  g.setGraph({ rankdir: direction });

  const size = (n: LayoutRequest['nodes'][number]) => ({
    width: n.width || DEFAULT_WIDTH,
    height: n.height || DEFAULT_HEIGHT,
  });
  for (const n of nodes) g.setNode(n.id, size(n));
  for (const e of edges) g.setEdge(e.source, e.target);

  dagre.layout(g);

  return {
    id,
    nodes: nodes.map((n) => {
      const pos = g.node(n.id);
      const { width, height } = size(n);
      return { id: n.id, x: (pos?.x ?? 0) - width / 2, y: (pos?.y ?? 0) - height / 2 };
    }),
  };
}
