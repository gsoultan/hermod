import { useHotkeys } from '@mantine/hooks';
import { useWorkflowStore } from '../store/useWorkflowStore';

export function useWorkflowHotkeys(
  handleSave: () => void,
  handleTest: (input: any, dryRun: boolean) => void
) {
  const { setNodes, setEdges, setSelectedNode } = useWorkflowStore();

  const isTypingTarget = (evt: any) => {
    const t = (evt?.target as HTMLElement) || null;
    if (!t) return false;
    const tag = t.tagName?.toLowerCase();
    return tag === 'input' || tag === 'textarea' || tag === 'select' || (t as any).isContentEditable;
  };

  useHotkeys([
    ['ctrl+s', (e) => { if (isTypingTarget(e)) return; e.preventDefault(); handleSave(); }],
    ['ctrl+enter', (e) => { if (isTypingTarget(e)) return; e.preventDefault(); handleTest(null, false); }],
    ['ctrl+shift+enter', (e) => { if (isTypingTarget(e)) return; e.preventDefault(); handleTest(null, true); }],
    // One key per entry: useHotkeys has no "a, b" alternative syntax, and
    // 'delete, backspace' parsed as a single key no keyboard sends, so Delete
    // did nothing. Backspace stays with React Flow, whose default delete key
    // it is.
    ['delete', (e) => {
       if (isTypingTarget(e)) return;
       const { nodes, edges } = useWorkflowStore.getState();
       const removed = new Set(nodes.filter(n => n.selected).map(n => n.id));
       const anySelected = removed.size > 0 || edges.some(e => e.selected);
       if (anySelected) {
          setNodes(nds => nds.filter(n => !n.selected));
          // A deleted node's edges go with it, as deleteNode does; left behind
          // they point at nothing and are saved that way.
          setEdges(eds => eds.filter(e => !e.selected && !removed.has(e.source) && !removed.has(e.target)));
          setSelectedNode(null);
       }
    }]
  ]);
}
