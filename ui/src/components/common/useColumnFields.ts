import { useCallback, useMemo } from 'react';

/**
 * The `column.*` subset of a transformation node's config — what the raw-JSON
 * panes edit — and a committer that replaces exactly that subset.
 *
 * Shared by the set and advanced config components so the JsonObjectInput
 * pane behaves identically in both, and derived once per config change rather
 * than re-serialised inside each pane's `value`.
 */
export function useColumnFields(
  config: Record<string, unknown> | undefined,
  nodeId: string,
  updateNodeConfig: (id: string, config: any, replace?: boolean) => void,
) {
  const columnFields = useMemo(
    () =>
      Object.fromEntries(
        Object.entries(config ?? {}).filter(([k]) => k.startsWith('column.')),
      ) as Record<string, unknown>,
    [config],
  );

  const replaceColumnFields = useCallback(
    (next: Record<string, unknown>) => {
      const rest = Object.fromEntries(
        Object.entries(config ?? {}).filter(([k]) => !k.startsWith('column.')),
      );
      updateNodeConfig(nodeId, { ...rest, ...next }, true);
    },
    [config, nodeId, updateNodeConfig],
  );

  return { columnFields, replaceColumnFields };
}
