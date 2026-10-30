import { useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import { apiFetch } from '@/api';

async function list(kind: 'sources' | 'sinks', vhost?: string) {
  const vhostParam = vhost && vhost !== 'all' ? `?vhost=${vhost}` : '';
  return (await apiFetch(`/api/${kind}${vhostParam}`)).json();
}

/**
 * Connection names by id, for calling a source or sink step by the connection
 * it uses when it has no label of its own. Shares the editor's source and
 * sink queries, so opening the editor after the view costs no extra fetch.
 */
export function useConnectionNames(vhost: string | undefined, enabled = true): Record<string, string> {
  const { data: sources } = useQuery({ queryKey: ['sources', vhost], queryFn: () => list('sources', vhost), enabled });
  const { data: sinks } = useQuery({ queryKey: ['sinks', vhost], queryFn: () => list('sinks', vhost), enabled });
  return useMemo(() => {
    const names: Record<string, string> = {};
    for (const c of [...((sources as any)?.data || []), ...((sinks as any)?.data || [])]) {
      if (c?.id && c.name) names[c.id] = c.name;
    }
    return names;
  }, [sources, sinks]);
}
