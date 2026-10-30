import { useEffect, useState } from 'react';
import { apiFetch } from '@/api';
import type { SinkOption } from '@/utils/txgroup';

/** The JSON of a response, or the value itself when a test stubbed apiFetch. */
const readJSON = (res: any) => (res && typeof res.json === 'function' ? res.json() : res);

/**
 * The stored sinks a group sink (transactional or failover) chooses from.
 * Neither the Sinks page nor the workflow editor's sink form passes them down,
 * which left those pickers empty; so when none are given, this asks. Null
 * while the list is loading.
 */
export function useSinkOptions(given?: any[]): SinkOption[] | null {
  const [fetched, setFetched] = useState<SinkOption[] | null>(null);
  const useGiven = Array.isArray(given) && given.length > 0;
  useEffect(() => {
    if (useGiven) return;
    let cancelled = false;
    apiFetch('/api/sinks?limit=1000', { silent: true })
      .then(readJSON)
      .then((data: any) => {
        const rows = Array.isArray(data?.data) ? data.data : Array.isArray(data) ? data : [];
        if (!cancelled) setFetched(rows);
      })
      .catch(() => {
        if (!cancelled) setFetched([]);
      });
    return () => {
      cancelled = true;
    };
  }, [useGiven]);
  const rows = useGiven ? given! : fetched;
  return rows === null ? null : rows.map((s: any) => ({ id: s.id, name: s.name, type: s.type }));
}
