import { useEffect, useRef, useState, useCallback } from 'react';
import type { DataRequest, DataResponse } from '../workers/dataWorker';

/**
 * useDataWorker
 * Hook to interact with the dataWorker for offloading heavy data operations.
 */
export function useDataWorker() {
  const workerRef = useRef<Worker | null>(null);
  const seqRef = useRef(0);
  const latestSeqRef = useRef(0);
  const [processing, setProcessing] = useState(false);

  useEffect(() => {
    return () => {
      if (workerRef.current) {
        workerRef.current.terminate();
        workerRef.current = null;
      }
    };
  }, []);

  const process = useCallback((type: DataRequest['type'], data: any[], params: any) => {
    return new Promise<any[]>((resolve) => {
      if (data.length === 0) {
        resolve([]);
        return;
      }

      // For very small datasets, avoid worker overhead and compute on main thread
      if (data.length < 50) {
        // Simple fallback implementation or just resolve immediately if we don't want to duplicate logic
        // But for consistency and simplicity in the hook consumer, we still use the worker
        // unless it's truly trivial.
      }

      if (!workerRef.current) {
        workerRef.current = new Worker(
          new URL('../workers/dataWorker.ts', import.meta.url),
          { type: 'module' }
        );
      }

      const id = ++seqRef.current;
      latestSeqRef.current = id;
      setProcessing(true);

      workerRef.current.onmessage = (ev: MessageEvent<DataResponse>) => {
        if (ev.data?.id === latestSeqRef.current) {
          setProcessing(false);
          resolve(ev.data.result);
        }
      };

      workerRef.current.onerror = (err) => {
        console.error('Data worker error:', err);
        setProcessing(false);
        resolve(data); // Fallback to original data
      };

      workerRef.current.postMessage({ id, type, data, params } satisfies DataRequest);
    });
  }, []);

  return { process, processing };
}
