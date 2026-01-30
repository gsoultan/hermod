import { useCallback, useEffect, useRef, useState } from 'react';
import { apiJson } from '../../../api';

export interface UseTargetSchemaParams {
  sinkSchema?: any;
}

export function useTargetSchema({ sinkSchema }: UseTargetSchemaParams) {
  const [fields, setFields] = useState<string[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const abortRef = useRef<AbortController | null>(null);

  const fetchSchema = useCallback(async () => {
    if (!sinkSchema?.config?.table) return;
    if (abortRef.current) {
      abortRef.current.abort();
    }
    const controller = new AbortController();
    abortRef.current = controller;
    setLoading(true);
    setError(null);
    try {
      const data = await apiJson<any>('/api/sinks/sample', {
        method: 'POST',
        body: JSON.stringify({
          sink: sinkSchema,
          table: sinkSchema.config.table,
        }),
        signal: controller.signal,
        headers: { 'Content-Type': 'application/json' },
      });
      if (data?.after) {
        const payload = JSON.parse(data.after);
        setFields(Object.keys(payload));
      } else {
        setFields([]);
      }
    } catch (e: any) {
      if (e?.name !== 'AbortError') {
        setError(e?.message || 'Failed to fetch target schema');
      }
    } finally {
      setLoading(false);
    }
  }, [sinkSchema]);

  useEffect(() => {
    fetchSchema();
    return () => {
      if (abortRef.current) abortRef.current.abort();
    };
  }, [fetchSchema]);

  return { fields, loading, error, refetch: fetchSchema };
}
