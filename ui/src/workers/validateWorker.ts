/// <reference lib="webworker" />

/**
 * validateWorker.ts
 * Offloads JSON validation and complex rule checking from the main thread.
 */

export type ValidateRequest = {
  id: number;
  payload: any;
  rules?: any;
};

export type ValidateResponse = {
  id: number;
  ok: boolean;
  errors?: string[];
};

// eslint-disable-next-line no-restricted-globals
self.onmessage = (e: MessageEvent<ValidateRequest>) => {
  const { id, payload, rules } = e.data;
  
  try {
    const result = validate(payload, rules);
    // eslint-disable-next-line no-restricted-globals
    (self as unknown as Worker).postMessage({ id, ...result } satisfies ValidateResponse);
  } catch (err) {
    // eslint-disable-next-line no-restricted-globals
    (self as unknown as Worker).postMessage({ 
      id, 
      ok: false, 
      errors: [err instanceof Error ? err.message : 'Unknown validation error'] 
    } satisfies ValidateResponse);
  }
};

/**
 * Synchronous validation logic.
 * Keep it pure and focused on CPU-bound checks.
 */
function validate(payload: any, rules?: any): { ok: boolean; errors?: string[] } {
  const errors: string[] = [];

  // 1. Basic JSON validity check (already parsed if coming from API, but just in case)
  if (!payload || typeof payload !== 'object') {
    return { ok: false, errors: ['Payload must be a valid JSON object'] };
  }

  // 2. Placeholder for more complex, CPU-intensive rules
  // Example: Check for circular references (slow for big objects)
  try {
    JSON.stringify(payload);
  } catch (err) {
    return { ok: false, errors: ['Circular reference detected in payload'] };
  }

  // 3. Custom rule evaluation (if provided)
  // This could involve nested property checks, large array validations, etc.
  if (rules && Array.isArray(rules)) {
    for (const rule of rules) {
      if (rule.type === 'required' && !payload[rule.field]) {
        errors.push(`Field "${rule.field}" is required`);
      }
      // Add more complex rule types here...
    }
  }

  return { 
    ok: errors.length === 0, 
    errors: errors.length > 0 ? errors : undefined 
  };
}
