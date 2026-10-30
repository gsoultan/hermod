import { useEffect, useState } from 'react';
import { apiFetch } from '@/api';

/** What the server says about two-phase commit here. */
export type TwoPhaseCapability = {
  /** Sink types that can take part in a transaction across databases. */
  types: string[];
  /** Whether a group could keep its transaction log through a restart. */
  logReady: boolean;
  /** The server's reason when it could not, or ''. */
  logProblem: string;
};

/** The JSON of a response, or the value itself when a test stubbed apiFetch. */
const readJSON = (res: any) => (res && typeof res.json === 'function' ? res.json() : res);

/**
 * Which sinks can take part in two-phase commit, and whether a group could
 * keep its transaction log here. Both come from the backend, which enforces the
 * same rules when the workflow starts, so the form and the engine cannot
 * disagree. Null while the answer is loading.
 */
export function useTwoPhaseCapability(): TwoPhaseCapability | null {
  const [capability, setCapability] = useState<TwoPhaseCapability | null>(null);
  useEffect(() => {
    let cancelled = false;
    apiFetch('/api/sinks/capabilities/two-phase', { silent: true })
      .then(readJSON)
      .then((data: any) => {
        if (cancelled) return;
        setCapability({
          types: Array.isArray(data?.types) ? data.types : [],
          // An older server does not report the log; say nothing rather than guess.
          logReady: data?.log_ready !== false,
          logProblem: typeof data?.log_problem === 'string' ? data.log_problem : '',
        });
      })
      .catch(() => {
        // Offer nothing rather than guess. A picker built from a hardcoded list
        // is how someone ends up with a group that cannot start.
        if (!cancelled) setCapability({ types: [], logReady: true, logProblem: '' });
      });
    return () => {
      cancelled = true;
    };
  }, []);
  return capability;
}
