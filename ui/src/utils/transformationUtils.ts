export const getAllKeys = (obj: any, prefix = ''): string[] => {
  if (!obj || typeof obj !== 'object' || obj === null) return [];
  return Object.keys(obj).reduce((acc: string[], key: string) => {
    const path = prefix ? `${prefix}.${key}` : key;
    acc.push(path);
    if (obj[key] && typeof obj[key] === 'object' && !Array.isArray(obj[key])) {
      acc.push(...getAllKeys(obj[key], path));
    }
    return acc;
  }, []);
};

export const getValByPath = (obj: any, path: string) => {
  if (!path) return undefined;
  
  // Handle basic path without special characters efficiently
  if (!path.includes('#') && !path.includes('|') && !path.includes('*') && !path.includes('?') && !path.includes('(')) {
    return path.split('.').reduce((acc, part) => {
      if (acc && typeof acc === 'string' && (acc.startsWith('{') || acc.startsWith('['))) {
        try {
          acc = JSON.parse(acc);
        } catch (e) {}
      }
      return acc && acc[part];
    }, obj);
  }

  // Fallback to basic dot notation for anything else in simulation
  // gjson and sjson support much more complex syntax that would require a full library to match perfectly.
  return path.split('.').reduce((acc, part) => {
    return acc && acc[part];
  }, obj);
};

// Simple template resolver mirroring backend ResolveTemplate for UI simulation
// Replaces tokens like {{.field.path}} with values from the payload. Also supports
// calling basic expressions inside {{ ... }} by delegating to parseAndEvaluate when it
// detects a function call (has parentheses at the end).
export const resolveTemplateStr = (tpl: string, source: any): string => {
  if (!tpl || typeof tpl !== 'string' || tpl.indexOf('{{') === -1) return String(tpl ?? '');
  let out = tpl;
  const re = /\{\{\s*([^}]+)\s*\}\}/g;
  out = out.replace(re, (_m, inner: string) => {
    const expr = String(inner || '').trim();
    try {
      // Function call (ends with ')') → evaluate
      if (expr.endsWith(')')) {
        const val = parseAndEvaluate(expr, source);
        return val == null ? '' : String(val);
      }
      // Path reference (optionally starting with '.')
      const p = expr.startsWith('.') ? expr.slice(1) : expr;
      const v = getValByPath(source, p);
      return v == null ? '' : String(v);
    } catch {
      return '';
    }
  });
  return out;
};

export const setValByPath = (obj: any, path: string, val: any) => {
  if (!path || !obj) return;
  const parts = path.split('.');
  const last = parts.pop()!;
  let target = obj;

  for (let i = 0; i < parts.length; i++) {
    const part = parts[i];
    
    // Support array append syntax (-1) from sjson
    if (part === '-1' && Array.isArray(target)) {
      // This is slightly tricky for setValByPath when -1 is in the middle
      // but sjson usually uses -1 at the end or to append to an array.
      // If it's in the middle, we assume the user wants to append an object/array.
      const newObj = {};
      target.push(newObj);
      target = newObj;
      continue;
    }

    const nextPart = i + 1 < parts.length ? parts[i+1] : last;
    const isNextNumber = !isNaN(Number(nextPart)) || nextPart === '-1';

    if (!target[part] || typeof target[part] !== 'object') {
      target[part] = isNextNumber ? [] : {};
    }
    target = target[part];
  }

  if (last === '-1' && Array.isArray(target)) {
    target.push(val);
  } else {
    target[last] = val;
  }
};

export const parseAndEvaluate = (expr: string, source: any): any => {
  expr = expr.trim();
  if (!expr) return null;

  // Check for function call
  if (expr.endsWith(')')) {
    let openParen = -1;
    let parenCount = 0;
    for (let i = expr.length - 1; i >= 0; i--) {
      if (expr[i] === ')') parenCount++;
      else if (expr[i] === '(') {
        parenCount--;
        if (parenCount === 0) {
          openParen = i;
          break;
        }
      }
    }

    if (openParen > 0) {
      const funcName = expr.substring(0, openParen).trim();
      const isFunc = /^[a-zA-Z0-9_]+$/.test(funcName);
      if (isFunc) {
        const argsStr = expr.substring(openParen + 1, expr.length - 1);
        const args = parseArgs(argsStr);
        const evaluatedArgs = args.map(arg => parseAndEvaluate(arg, source));
        return callFunction(funcName, evaluatedArgs);
      }
    }
  }

  // String literals
  if ((expr.startsWith('"') && expr.endsWith('"')) || (expr.startsWith("'") && expr.endsWith("'"))) {
    return expr.substring(1, expr.length - 1);
  }

  // Source reference
  if (expr.startsWith('source.')) {
    return getValByPath(source, expr.substring(7));
  }

  // Numbers
  if (!isNaN(Number(expr)) && expr !== '') {
    return Number(expr);
  }

  return expr;
};

const parseArgs = (argsStr: string): string[] => {
  const args: string[] = [];
  let current = '';
  let parenCount = 0;
  let inQuotes = false;
  let quoteChar = '';

  for (let i = 0; i < argsStr.length; i++) {
    const c = argsStr[i];
    if ((c === '"' || c === "'") && (i === 0 || argsStr[i - 1] !== '\\')) {
      if (!inQuotes) {
        inQuotes = true;
        quoteChar = c;
      } else if (c === quoteChar) {
        inQuotes = false;
      }
      current += c;
    } else if (!inQuotes && c === '(') {
      parenCount++;
      current += c;
    } else if (!inQuotes && c === ')') {
      parenCount--;
      current += c;
    } else if (!inQuotes && parenCount === 0 && c === ',') {
      args.push(current.trim());
      current = '';
    } else {
      current += c;
    }
  }
  if (current.trim() || args.length > 0) {
    args.push(current.trim());
  }
  return args;
};

const toBool = (val: any): boolean => {
  if (val === null || val === undefined) return false;
  if (typeof val === 'boolean') return val;
  if (typeof val === 'string') {
    const s = val.toLowerCase();
    if (['true', '1', 'yes', 'on'].includes(s)) return true;
    if (['false', '0', 'no', 'off'].includes(s)) return false;
  }
  if (typeof val === 'number') return val !== 0;
  return !!val;
};

const callFunction = (name: string, args: any[]): any => {
  switch (name.toLowerCase()) {
    case 'lower': return String(args[0] || '').toLowerCase();
    case 'upper': return String(args[0] || '').toUpperCase();
    case 'trim': return String(args[0] || '').trim();
    case 'replace': return String(args[0] || '').split(String(args[1] || '')).join(String(args[2] || ''));
    case 'concat': return args.join('');
    case 'substring': {
      const s = String(args[0] || '');
      const start = Number(args[1]) || 0;
      const end = args[2] !== undefined ? Number(args[2]) : s.length;
      return s.substring(start, end);
    }
    case 'date_format': {
      const dateStr = String(args[0] || '');
      // const toFormat = String(args[1] || ''); // Not used in simple fallback
      // Simple JS date formatting (won't match Go exactly but gives a preview)
      try {
        const d = new Date(dateStr);
        if (isNaN(d.getTime())) return dateStr;
        return d.toISOString().split('T')[0]; // Simple fallback for preview
      } catch (e) { return dateStr; }
    }
    case 'coalesce': return args.find(a => a !== null && a !== undefined && a !== '');
    case 'now': return new Date().toISOString();
    case 'hash': return '[HASH]';
    case 'add': return (Number(args[0]) || 0) + (Number(args[1]) || 0);
    case 'sub': return (Number(args[0]) || 0) - (Number(args[1]) || 0);
    case 'mul': return (Number(args[0]) || 0) * (Number(args[1]) || 0);
    case 'div': return (Number(args[1]) || 0) !== 0 ? (Number(args[0]) || 0) / (Number(args[1]) || 0) : 0;
    case 'abs': return Math.abs(Number(args[0]) || 0);
    case 'round': {
      const v = Number(args[0]) || 0;
      const precision = Number(args[1]) || 0;
      const ratio = Math.pow(10, precision);
      return Math.round(v * ratio) / ratio;
    }
    case 'and': return args.every(a => toBool(a));
    case 'or': return args.some(a => toBool(a));
    case 'not': return !toBool(args[0]);
    case 'if': return toBool(args[0]) ? args[1] : args[2];
    case 'eq': return String(args[0]) === String(args[1]);
    case 'gt': {
      const v1 = Number(args[0]);
      const v2 = Number(args[1]);
      if (!isNaN(v1) && !isNaN(v2)) return v1 > v2;
      return String(args[0]) > String(args[1]);
    }
    case 'lt': {
      const v1 = Number(args[0]);
      const v2 = Number(args[1]);
      if (!isNaN(v1) && !isNaN(v2)) return v1 < v2;
      return String(args[0]) < String(args[1]);
    }
    case 'contains': return String(args[0] || '').includes(String(args[1] || ''));
    case 'toint': return Math.floor(Number(args[0]) || 0);
    case 'tofloat': return Number(args[0]) || 0;
    case 'tostring': return String(args[0]);
    case 'tobool': return toBool(args[0]);
    default: return null;
  }
};

export interface Condition {
  field: string;
  operator: string;
  value: any;
}

export const matchesCondition = (payload: any, cond: Condition): boolean => {
  const fieldVal = getValByPath(payload, cond.field);
  const op = cond.operator || '=';
  // Resolve value templates (e.g., {{.after.id}} or {{upper(source.name)}})
  const rawVal = cond.value as any;
  const val = typeof rawVal === 'string' && rawVal.includes('{{') ? resolveTemplateStr(rawVal, payload) : rawVal;

  if (['>', '>=', '<', '<='].includes(op)) {
    const v1 = Number(fieldVal);
    const v2 = Number(val);
    if (!isNaN(v1) && !isNaN(v2)) {
      switch (op) {
        case '>': return v1 > v2;
        case '>=': return v1 >= v2;
        case '<': return v1 < v2;
        case '<=': return v1 <= v2;
      }
    }
  }

  const s1 = String(fieldVal ?? '');
  const s2 = String(val ?? '');

  switch (op) {
    case '=': return s1 === s2;
    case '!=': return s1 !== s2;
    case 'contains': return s1.includes(s2);
    case 'regex': {
      try { return new RegExp(s2).test(s1); } catch { return false; }
    }
    case 'not_regex': {
      try { return !new RegExp(s2).test(s1); } catch { return false; }
    }
    case '>': return s1 > s2;
    case '>=': return s1 >= s2;
    case '<': return s1 < s2;
    case '<=': return s1 <= s2;
    default: return false;
  }
};

export interface SimulationResult {
  output: any;
  metadata: {
    isFiltered: boolean;
    filterReason?: string;
    matchedLabel?: string;
  };
}

export const simulateTransformation = (transType: string, data: any, inputPayload: any): SimulationResult => {
  const result: SimulationResult = {
    output: inputPayload ? JSON.parse(JSON.stringify(inputPayload)) : null,
    metadata: { isFiltered: false }
  };

  if (!inputPayload) return result;
  
  try {
    if (transType === 'mask' && data.field) {
       const val = String(getValByPath(result.output, data.field) || '');
       let masked = "****";
       if (data.maskType === 'email') {
          const parts = val.split('@');
          masked = parts.length === 2 ? (parts[0].length > 1 ? parts[0][0] + "****@" + parts[1] : "*@" + parts[1]) : "****";
       } else if (data.maskType === 'partial' && val.length > 4) {
          masked = val.substring(0, 2) + "****" + val.substring(val.length - 2);
       }
       setValByPath(result.output, data.field, masked);
    } else if (transType === 'mapping' && data.field && data.mapping) {
       const fieldValRaw = getValByPath(result.output, data.field);
       const val = String(fieldValRaw || '');
       let mapping = data.mapping;
       if (typeof mapping === 'string') {
         try { mapping = JSON.parse(mapping); } catch(e) { mapping = {}; }
       }
       
       const mappingType = data.mappingType || 'exact';
       if (mappingType === 'range') {
         const numVal = Number(fieldValRaw);
         if (!isNaN(numVal)) {
           for (const [k, v] of Object.entries(mapping)) {
             if (k.includes('-')) {
               const [low, high] = k.split('-').map(Number);
               if (numVal >= low && numVal <= high) {
                 setValByPath(result.output, data.field, v);
                 break;
               }
             } else if (k.endsWith('+')) {
               const low = Number(k.replace('+', ''));
               if (numVal >= low) {
                 setValByPath(result.output, data.field, v);
                 break;
               }
             }
           }
         }
       } else if (mappingType === 'regex') {
         for (const [k, v] of Object.entries(mapping)) {
           try {
             const re = new RegExp(k);
             if (re.test(val)) {
               setValByPath(result.output, data.field, v);
               break;
             }
           } catch(e) {}
         }
       } else {
         if (mapping && mapping[val] !== undefined) {
            setValByPath(result.output, data.field, mapping[val]);
         }
       }
    } else if (transType === 'filter_data' || transType === 'condition' || transType === 'validate') {
       let conditions: any[] = [];
       if (data.conditions) {
         try {
           conditions = typeof data.conditions === 'string' ? JSON.parse(data.conditions) : data.conditions;
         } catch(e) { conditions = []; }
       }
       
       if (conditions.length === 0 && data.field) {
         conditions.push({
           field: data.field,
           operator: data.operator || '=',
           value: data.value || ''
         });
       }

       let allMatch = true;
       for (const cond of conditions) {
         if (!matchesCondition(result.output, cond)) {
           allMatch = false;
           result.metadata.filterReason = `${transType === 'condition' ? 'Condition' : 'Filter'}: ${cond.field} (${getValByPath(result.output, cond.field)}) ${cond.operator} ${cond.value} is false`;
           break;
         }
       }
       
       if (transType === 'filter_data') {
         if (data.asField) {
           const targetField = data.targetField || 'is_valid';
           setValByPath(result.output, targetField, allMatch);
         } else if (!allMatch) {
           result.metadata.isFiltered = true;
           result.output = null;
         }
       } else if (transType === 'validate') {
         const targetField = data.targetField || 'is_valid';
         setValByPath(result.output, targetField, allMatch);
       } else if (transType === 'condition') {
         result.metadata.matchedLabel = allMatch ? 'true' : 'false';
         result.metadata.filterReason = `Branch: ${result.metadata.matchedLabel} (${result.metadata.filterReason || 'all conditions met'})`;
       }
    } else if (transType === 'set' || transType === 'advanced') {
       const source = result.output;
       if (transType === 'advanced') {
         result.output = {};
       }
       Object.entries(data).forEach(([k, v]) => {
         if (k.startsWith('column.')) {
           const colPath = k.replace('column.', '');
           let val = v;
           if (typeof v === 'string') {
              val = parseAndEvaluate(v, source);
           }
           setValByPath(result.output, colPath, val);
         }
       });
    } else if (transType === 'stateful') {
       const op = data.operation || 'count';
       const field = data.field;
       const outputField = data.outputField || `${field}_${op}`;
       let currentVal = op === 'count' ? 1 : Number(getValByPath(result.output, field) || 0);
       setValByPath(result.output, outputField, currentVal);
    } else if (transType === 'pipeline') {
       let steps = data.steps;
       if (typeof steps === 'string') {
          try { steps = JSON.parse(steps); } catch(e) { steps = []; }
       }
       if (Array.isArray(steps)) {
          steps.forEach(step => {
             const stepRes = simulateTransformation(step.transType, step, result.output);
             result.output = stepRes.output;
             if (stepRes.metadata.isFiltered) {
               result.metadata.isFiltered = true;
               result.metadata.filterReason = `Pipeline step filtered: ${stepRes.metadata.filterReason}`;
             }
          });
       }
    } else if (transType === 'switch') {
      let cases: any[] = [];
      try {
        cases = typeof data.cases === 'string' ? JSON.parse(data.cases) : (data.cases || []);
      } catch(e) {}

      let matchedLabel = "default";
      for (const c of cases) {
        if (c.conditions && c.conditions.length > 0) {
          let caseMatch = true;
          for (const cond of c.conditions) {
            if (!matchesCondition(result.output, cond)) {
              caseMatch = false;
              break;
            }
          }
          if (caseMatch) {
            matchedLabel = c.label;
            break;
          }
        } else {
          const fieldVal = String(getValByPath(result.output, data.field) || '');
          if (fieldVal === String(c.value)) {
            matchedLabel = c.label;
            break;
          }
        }
      }
      result.metadata.matchedLabel = matchedLabel;
      result.metadata.filterReason = `Switch Branch: ${matchedLabel}`;
    } else if (transType === 'db_lookup') {
       if (data.targetField) {
          setValByPath(result.output, data.targetField, data.defaultValue ? `[DB Lookup Result (or ${data.defaultValue})]` : `[DB Lookup Result]`);
       }
    } else if (transType === 'api_lookup') {
       if (data.targetField) {
          setValByPath(result.output, data.targetField, data.defaultValue ? `[API Lookup Result (or ${data.defaultValue})]` : `[API Lookup Result]`);
       }
    }
  } catch (e) {
    // Ignore simulation errors
  }
  return result;
};

export const preparePayload = (payload: any) => {
  if (!payload || typeof payload !== 'object' || payload === null) return payload;
  const result = Array.isArray(payload) ? [...payload] : { ...payload };
  
  if (!Array.isArray(result)) {
    ['after', 'before'].forEach(key => {
      const val = result[key];
      let nested = null;
      if (typeof val === 'string' && val.trim().startsWith('{')) {
        try {
          nested = JSON.parse(val);
          result[key] = nested;
        } catch (e) {}
      } else if (val && typeof val === 'object' && !Array.isArray(val)) {
        nested = val;
      }
    });

    // Enrich CDC metadata for simulator/UX: expose operation/table/schema at root
    const hasCDC = !!(result as any).after || !!(result as any).before;
    if (hasCDC) {
      // Map Debezium-style op codes to readable operations if possible
      const opVal = (result as any).operation || (result as any).op;
      if (!('operation' in result) && typeof opVal === 'string') {
        const map: Record<string, string> = { c: 'create', u: 'update', d: 'delete', r: 'snapshot' };
        (result as any).operation = map[opVal] || opVal;
      }
      // Surface table/schema from common locations (e.g., source.table)
      const src = (result as any).source || {};
      if (!('table' in result) && typeof src.table === 'string') {
        (result as any).table = src.table;
      }
      if (!('schema' in result) && typeof src.schema === 'string') {
        (result as any).schema = src.schema;
      }
    }
  }
  return result;
};

export const deepMergeSim = (dst: any, src: any, strategy: string = 'deep') => {
  if (!src || typeof src !== 'object') return;
  if (!dst || typeof dst !== 'object') return;

  switch (strategy) {
    case 'overwrite':
      Object.keys(src).forEach(key => {
        dst[key] = src[key];
      });
      break;
    case 'if_missing':
      Object.keys(src).forEach(key => {
        if (dst[key] === undefined) {
          dst[key] = src[key];
        }
      });
      break;
    case 'shallow':
      Object.keys(src).forEach(key => {
        dst[key] = src[key];
      });
      break;
    case 'deep':
    default:
      Object.keys(src).forEach(key => {
        if (src[key] && typeof src[key] === 'object' && !Array.isArray(src[key])) {
          if (!dst[key] || typeof dst[key] !== 'object') {
            dst[key] = {};
          }
          deepMergeSim(dst[key], src[key], 'deep');
        } else {
          dst[key] = src[key];
        }
      });
      break;
  }
};
