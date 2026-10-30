/// <reference lib="webworker" />
// Web worker to run Dagre layout off the main thread
import { layoutPositions, type LayoutRequest, type LayoutResponse } from './layoutPositions';

export type { LayoutRequest, LayoutResponse };

// eslint-disable-next-line no-restricted-globals
self.onmessage = (e: MessageEvent<LayoutRequest>) => {
  const { id, nodes } = e.data;

  try {
    // eslint-disable-next-line no-restricted-globals
    (self as unknown as Worker).postMessage(layoutPositions(e.data));
  } catch {
    // On error, just return zero positions so caller can fallback
    // eslint-disable-next-line no-restricted-globals
    (self as unknown as Worker).postMessage({ id, nodes: nodes.map((n) => ({ id: n.id, x: 0, y: 0 })) } as LayoutResponse);
  }
};
