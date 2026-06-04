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
    ['delete, backspace', (e) => {
       if (isTypingTarget(e)) return;
       const { nodes, edges } = useWorkflowStore.getState();
       const anySelected = nodes.some(n => n.selected) || edges.some(e => e.selected);
       if (anySelected) {
          setNodes(nds => nds.filter(n => !n.selected));
          setEdges(eds => eds.filter(e => !e.selected));
          setSelectedNode(null);
       }
    }]
  ]);
}
