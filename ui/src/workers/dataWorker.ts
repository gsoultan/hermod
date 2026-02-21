/// <reference lib="webworker" />

/**
 * dataWorker.ts
 * Offloads heavy data processing tasks like filtering, sorting, and searching 
 * from the main thread to keep the UI responsive.
 */

export type DataRequest = {
  id: number;
  type: 'filter' | 'sort' | 'search';
  data: any[];
  params: any;
};

export type DataResponse = {
  id: number;
  result: any[];
};

// eslint-disable-next-line no-restricted-globals
self.onmessage = (e: MessageEvent<DataRequest>) => {
  const { id, type, data, params } = e.data;
  
  try {
    let result: any[] = [];
    
    switch (type) {
      case 'filter':
        result = handleFilter(data, params);
        break;
      case 'sort':
        result = handleSort(data, params);
        break;
      case 'search':
        result = handleSearch(data, params);
        break;
      default:
        result = data;
    }
    
    // eslint-disable-next-line no-restricted-globals
    (self as unknown as Worker).postMessage({ id, result } satisfies DataResponse);
  } catch (err) {
    console.error('Data worker processing error:', err);
    // Fallback to returning original data on error to prevent UI crash
    // eslint-disable-next-line no-restricted-globals
    (self as unknown as Worker).postMessage({ id, result: data } satisfies DataResponse);
  }
};

function handleFilter(data: any[], params: { filters?: { field: string; value: any }[]; search?: { query: string; fields?: string[] } }): any[] {
  let result = data;

  if (params.filters && params.filters.length > 0) {
    result = result.filter(item => {
      return params.filters!.every(p => {
        if (p.value === 'all') return true;
        return item[p.field] === p.value;
      });
    });
  }

  if (params.search && params.search.query) {
    const { query, fields } = params.search;
    const lowerQuery = query.toLowerCase();
    
    result = result.filter(item => {
      if (fields && fields.length > 0) {
        return fields.some(f => {
          const val = item[f];
          return val != null && String(val).toLowerCase().includes(lowerQuery);
        });
      }
      try {
        return JSON.stringify(item).toLowerCase().includes(lowerQuery);
      } catch {
        return false;
      }
    });
  }

  return result;
}

function handleSort(data: any[], params: { field: string; direction: 'asc' | 'desc' }): any[] {
  const { field, direction } = params;
  if (!field) return data;
  
  return [...data].sort((a, b) => {
    const valA = a[field];
    const valB = b[field];
    
    if (valA < valB) return direction === 'asc' ? -1 : 1;
    if (valA > valB) return direction === 'asc' ? 1 : -1;
    return 0;
  });
}

function handleSearch(data: any[], params: { query: string; fields?: string[] }): any[] {
  const { query, fields } = params;
  if (!query) return data;
  
  const lowerQuery = query.toLowerCase();
  
  return data.filter(item => {
    // If specific fields are provided, search only those
    if (fields && fields.length > 0) {
      return fields.some(f => {
        const val = item[f];
        return val != null && String(val).toLowerCase().includes(lowerQuery);
      });
    }
    
    // Otherwise, stringify the whole item and search (slow, but exhaustive)
    try {
      return JSON.stringify(item).toLowerCase().includes(lowerQuery);
    } catch {
      return false;
    }
  });
}
