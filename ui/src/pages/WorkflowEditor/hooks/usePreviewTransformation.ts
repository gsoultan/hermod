import { useEffect, useRef, useCallback, useMemo } from 'react';
import { useMutation } from '@tanstack/react-query';
import { apiJson } from '../../../api';

interface PreviewVars {
  transformation: {
    type: string;
    config: any;
  };
  message: any;
}

export function usePreviewTransformation() {
  const abortRef = useRef<AbortController | null>(null);

  const mutation = useMutation({
    mutationFn: async (vars: PreviewVars) => {
      // Cancel any in-flight preview before starting a new one
      if (abortRef.current) {
        abortRef.current.abort();
      }
      const controller = new AbortController();
      abortRef.current = controller;
      try {
        return await apiJson<any>('/api/transformations/test', {
          method: 'POST',
          body: JSON.stringify(vars),
          signal: controller.signal,
          headers: { 'Content-Type': 'application/json' },
        });
      } finally {
        // Clear controller when the request settles
        abortRef.current = null;
      }
    },
  });

  // Abort on unmount to avoid setting state on unmounted component
  useEffect(() => {
    return () => {
      if (abortRef.current) {
        abortRef.current.abort();
      }
    };
  }, []);

  const cancel = useCallback(() => {
    if (abortRef.current) {
      abortRef.current.abort();
    }
  }, []);

  return useMemo(() => ({ ...mutation, cancel }), [mutation, cancel]);
}
