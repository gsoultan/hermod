import { useEffect, useRef, useState, useCallback } from 'react';
import type { ValidateRequest, ValidateResponse } from '../workers/validateWorker';

/**
 * useValidateWorker
 * Hook to interact with the validateWorker using a promise-based API.
 * Handles worker instantiation, termination on unmount, and race conditions.
 */
export function useValidateWorker() {
  const workerRef = useRef<Worker | null>(null);
  const seqRef = useRef(0);
  const latestSeqRef = useRef(0);
  const [pending, setPending] = useState(false);

  useEffect(() => {
    // Terminate worker on cleanup
    return () => {
      if (workerRef.current) {
        workerRef.current.terminate();
        workerRef.current = null;
      }
    };
  }, []);

  const validate = useCallback((payload: any, rules?: any) => {
    return new Promise<ValidateResponse>((resolve) => {
      if (!workerRef.current) {
        // Instantiate lazily on first call
        workerRef.current = new Worker(
          new URL('../workers/validateWorker.ts', import.meta.url),
          { type: 'module' }
        );
      }

      const id = ++seqRef.current;
      latestSeqRef.current = id;
      setPending(true);

      workerRef.current.onmessage = (ev: MessageEvent<ValidateResponse>) => {
        // Only accept the latest result to avoid race conditions
        if (ev.data?.id === latestSeqRef.current) {
          setPending(false);
          resolve(ev.data);
        }
      };

      workerRef.current.onerror = (err) => {
        console.error('Validation worker error:', err);
        setPending(false);
        resolve({ id, ok: false, errors: ['Worker failure during validation'] });
      };

      workerRef.current.postMessage({ id, payload, rules } satisfies ValidateRequest);
    });
  }, []);

  return { validate, pending };
}
