import { useState, useEffect, useCallback, useRef } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useForm, useStore } from '@tanstack/react-form';
import { notifications } from '@mantine/notifications';
import { apiFetch } from '@/api';
import { humanizeError } from '@/api/errors';
import type { Sink, Workflow } from '@/types';

const API_BASE = '/api';

interface UseSinkFormProps {
  initialData?: Sink;
  isEditing?: boolean;
  embedded?: boolean;
  onSave?: (data: any) => void;
  vhost?: string;
  workerID?: string;
}

export function useSinkForm({ initialData, isEditing = false, embedded = false, onSave, vhost, workerID }: UseSinkFormProps) {
  const queryClient = useQueryClient();
  const [testResult, setTestResult] = useState<{ status: 'ok' | 'error', message: string } | null>(null);
  const [tables, setTables] = useState<string[]>([]);
  const [loadingTables, setLoadingTables] = useState(false);
  const [tablesError, setTablesError] = useState<string | null>(null);
  const [discoveredDatabases, setDiscoveredDatabases] = useState<string[]>([]);
  const [isFetchingDBs, setIsFetchingDBs] = useState(false);

  const dbAbortRef = useRef<AbortController | null>(null);
  const tablesAbortRef = useRef<AbortController | null>(null);

  const form = useForm({
    defaultValues: {
      name: initialData?.name || '', 
      type: initialData?.type || 'stdout', 
      vhost: (embedded ? vhost : (initialData?.vhost || vhost)) || '', 
      worker_id: (embedded ? workerID : (initialData?.worker_id || workerID)) || '',
      active: initialData?.active ?? true,
      config: { 
        format: 'json', 
        max_retries: '3', 
        retry_interval: '1s',
        sequential: initialData?.config?.sequential ?? false,
        ...(initialData?.config || {})
      },
      ...(initialData?.id ? { id: initialData.id } : {})
    }
  });

  const sink = useStore(form.store, (state) => state.values) as any;

  const { data: referencingData, isLoading: isLoadingRefWf, error: referencingError } = useQuery({
    queryKey: ['sink-workflows', sink.id],
    enabled: Boolean(isEditing && sink.id),
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/sinks/${sink.id}/workflows`);
      if (!res.ok) {
        const err = await res.json().catch(() => ({} as any));
        throw new Error(err.error || 'Failed to load referencing workflows');
      }
      return res.json();
    },
  });
  const referencingWorkflows: Workflow[] = (referencingData?.data as Workflow[]) || [];
  const hasActiveReferencingWorkflow = referencingWorkflows.some(w => w.active);

  const fetchDatabases = async () => {
    if (dbAbortRef.current) dbAbortRef.current.abort();
    const controller = new AbortController();
    dbAbortRef.current = controller;
    setIsFetchingDBs(true);
    try {
      const res = await apiFetch(`${API_BASE}/sinks/discover/databases`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(sink),
        signal: controller.signal,
      });
      const dbs = await res.json();
      if (!res.ok) throw new Error(dbs.error || 'Failed to discover databases');
      setDiscoveredDatabases(dbs || []);
    } catch (err: any) {
      if (err?.name !== 'AbortError') {
        setTestResult({ status: 'error', message: err.message });
      }
    } finally {
      setIsFetchingDBs(false);
    }
  };

  const lastDiscoveryParams = useRef<string>('');
  const discoverTables = useCallback(async (force = false) => {
    const params = JSON.stringify({
      type: sink.type,
      host: sink.config?.host,
      port: sink.config?.port,
      user: sink.config?.user,
      dbname: sink.config?.dbname,
      connection_string: sink.config?.connection_string,
      uri: sink.config?.uri,
      db_path: sink.config?.db_path,
      schema: sink.config?.schema,
    });

    if (!force && params === lastDiscoveryParams.current) return;
    lastDiscoveryParams.current = params;

    if (tablesAbortRef.current) tablesAbortRef.current.abort();
    const controller = new AbortController();
    tablesAbortRef.current = controller;
    setLoadingTables(true);
    setTablesError(null);
    try {
      const res = await apiFetch(`${API_BASE}/sinks/discover/tables`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(sink),
        signal: controller.signal,
      });
      const data = await res.json();
      if (!res.ok) throw new Error(data.error || 'Failed to discover tables');
      setTables(data || []);
    } catch (err: any) {
      if (err?.name !== 'AbortError') {
        setTablesError(err.message);
      }
    } finally {
      setLoadingTables(false);
    }
  }, [sink]);

  useEffect(() => {
    const dbTypes = ['postgres', 'mysql', 'mariadb', 'mssql', 'oracle', 'yugabyte', 'cassandra', 'sqlite', 'clickhouse', 'mongodb'];
    const needsDb = ['postgres', 'yugabyte', 'mysql', 'mariadb', 'mssql', 'oracle'].includes(sink.type);
    const hasConn = Boolean(sink.config?.host || sink.config?.connection_string || sink.config?.uri || sink.config?.db_path);
    if (dbTypes.includes(sink.type) && hasConn && (!needsDb || sink.config?.dbname)) {
      const timer = setTimeout(() => {
        discoverTables();
      }, 800);
      return () => {
        clearTimeout(timer);
        if (tablesAbortRef.current) tablesAbortRef.current.abort();
      };
    }
  }, [sink.type, sink.config?.host, sink.config?.connection_string, sink.config?.uri, sink.config?.db_path, sink.config?.dbname, discoverTables]);

  const testMutation = useMutation({
    mutationFn: async (s: any) => {
      const res = await apiFetch(`${API_BASE}/sinks/test`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(s),
      });
      const data = await res.json();
      if (!res.ok) throw new Error(data.error || 'Connection failed');
      return data;
    },
    onSuccess: () => {
      setTestResult({ status: 'ok', message: 'Connection successful!' });
    },
    onError: (error: Error) => {
      setTestResult({ status: 'error', message: error.message });
    }
  });

  const submitMutation = useMutation({
    mutationFn: async (s: any) => {
      const id = s.id || initialData?.id;
      const isUpdate = Boolean(isEditing || (id && id !== 'new'));
      const res = await apiFetch(`${API_BASE}/sinks${isUpdate && id ? `/${id}` : ''}`, {
        method: isUpdate ? 'PUT' : 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(s),
      });
      if (!res.ok) {
        const data = await res.json();
        throw new Error(data.error || `Failed to ${isUpdate ? 'update' : 'create'} sink`);
      }
      return res.json();
    },
    onSuccess: (data) => {
      queryClient.invalidateQueries({ queryKey: ['sinks'] });
      notifications.show({
        title: 'Success',
        message: `Sink ${isEditing ? 'updated' : 'created'} successfully`,
        color: 'green',
      });
      if (onSave) onSave(data);
    },
    onError: (error: Error) => {
      const hErr = humanizeError(error);
      notifications.show({
        title: hErr.title,
        message: hErr.message,
        color: hErr.color as any,
      });
    }
  });

  const updateConfig = (key: string, value: any) => {
    form.setFieldValue(`config.${key}` as any, value);
  };

  const handleSinkChange = (field: string, value: any) => {
    form.setFieldValue(field as any, value);
  };

  return {
    form,
    sink,
    testResult,
    setTestResult,
    tables,
    loadingTables,
    tablesError,
    discoveredDatabases,
    isFetchingDBs,
    fetchDatabases,
    discoverTables,
    testMutation,
    submitMutation,
    updateConfig,
    handleSinkChange,
    isLoadingRefWf,
    referencingError,
    hasActiveReferencingWorkflow,
    referencingWorkflows
  };
}
