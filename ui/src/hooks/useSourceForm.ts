import { useState, useEffect, useRef } from 'react';
import { useForm } from '@tanstack/react-form';
import { useStore } from '@tanstack/react-form';
import { useMutation, useQuery } from '@tanstack/react-query';
import { apiFetch } from '../api';
import { notifications } from '@mantine/notifications';
import { useNavigate } from '@tanstack/react-router';
import type { Source, Workflow } from '../types';

const API_BASE = '/api';

interface UseSourceFormProps {
  initialData?: Source;
  isEditing?: boolean;
  embedded?: boolean;
  onSave?: (data: any) => void;
  vhost?: string;
  workerID?: string;
}

export function useSourceForm({
  initialData,
  isEditing = false,
  embedded = false,
  onSave,
  vhost,
  workerID
}: UseSourceFormProps) {
  const navigate = useNavigate();
  const [testResult, setTestResult] = useState<{ status: 'ok' | 'error', message: string } | null>(null);
  const form = useForm({
    defaultValues: {
      name: initialData?.name || '', 
      type: initialData?.type || 'postgres', 
      vhost: (embedded ? vhost : (initialData?.vhost || vhost)) || '', 
      worker_id: (embedded ? workerID : (initialData?.worker_id || workerID)) || '',
      active: initialData?.active ?? true,
      config: { 
        connection_string: '',
        host: '',
        port: '',
        user: '',
        password: '',
        dbname: '',
        tables: '',
        use_cdc: 'true',
        sslmode: 'disable',
        slot_name: 'hermod_slot',
        publication_name: 'hermod_pub',
        reconnect_intervals: '30s',
        ...(initialData?.config || {})
      },
      ...(initialData?.id ? { id: initialData.id } : {})
    }
  });

  const source = useStore(form.store, (state) => state.values);

  const [discoveredDatabases, setDiscoveredDatabases] = useState<string[]>([]);
  const [discoveredTables, setDiscoveredTables] = useState<string[]>([]);
  const [isFetchingDBs, setIsFetchingDBs] = useState(false);
  const [isFetchingTables, setIsFetchingTables] = useState(false);
  const [uploading, setUploading] = useState(false);
  const [sampleData, setSampleData] = useState<Record<string, any> | null>(null);
  const [isFetchingSample, setIsFetchingSample] = useState(false);
  const [sampleError, setSampleError] = useState<string | null>(null);
  const [testInput, setTestInput] = useState<string>('');
  const [selectedSampleTable, setSelectedSampleTable] = useState<string>('');
  const [showSetup, setShowSetup] = useState(false);
  const [formPreviewOpened, setFormPreviewOpened] = useState(false);
  const [cdcReusePrompt, setCdcReusePrompt] = useState<null | {
    slot?: { name: string; exists: boolean; active?: boolean; hermod_in_use: boolean };
    publication?: { name: string; exists: boolean; hermod_in_use: boolean };
  }>(null);

  const [selectedSnapshotTables, setSelectedSnapshotTables] = useState<string[]>([]);
  const [snapshotModalOpened, setSnapshotModalOpened] = useState(false);

  const { data: referencingData, isLoading: isLoadingRefWf, error: referencingError } = useQuery({
    queryKey: ['source-workflows', source.id],
    enabled: Boolean(isEditing && source.id),
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/sources/${source.id}/workflows`);
      if (!res.ok) {
        const err = await res.json().catch(() => ({} as any));
        throw new Error(err.error || 'Failed to load referencing workflows');
      }
      return res.json();
    },
  });
  const referencingWorkflows: Workflow[] = (referencingData?.data as Workflow[]) || [];
  const hasActiveReferencingWorkflow = referencingWorkflows.some(w => w.active);

  const snapshotMutation = useMutation({
    mutationFn: async ({ sourceId, tables }: { sourceId: string, tables?: string[] }) => {
      const res = await apiFetch(`/api/sources/${sourceId}/snapshot`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ tables }),
      });
      if (!res.ok) {
        const err = await res.json();
        throw new Error(err.error || 'Failed to trigger snapshot');
      }
      return res.json();
    },
    onSuccess: () => {
      notifications.show({
        title: 'Snapshot Triggered',
        message: 'The initial snapshot has been started successfully.',
        color: 'green',
      });
      setSnapshotModalOpened(false);
    },
    onError: (error: Error) => {
      notifications.show({
        title: 'Snapshot Failed',
        message: error.message,
        color: 'red',
      });
    },
  });

  const isCDC = (type: string) => {
    return ['postgres', 'mysql', 'mssql', 'oracle', 'mongodb', 'cassandra', 'yugabyte', 'scylladb', 'clickhouse', 'sqlite', 'mariadb', 'db2', 'csv'].includes(type);
  };

  const isDatabaseSource = (type: string) => {
    return ['postgres', 'mysql', 'mssql', 'oracle', 'mongodb', 'yugabyte', 'mariadb', 'db2', 'cassandra', 'scylladb', 'clickhouse', 'sqlite', 'eventstore'].includes(type);
  };

  const fetchSample = async (s: any) => {
    let table = selectedSampleTable;
    if (!table && s.config.tables) {
      table = s.config.tables.split(',')[0].trim();
    }
    
    if (!isCDC(s.type) && testInput) {
      try {
        const data = JSON.parse(testInput);
        setSampleData(data);
        setSampleError(null);
        return;
      } catch (e) {}
    }

    setIsFetchingSample(true);
    setSampleError(null);
    try {
      const res = await apiFetch(`${API_BASE}/sources/sample`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ source: s, table }),
      });
      if (res.ok) {
        const data = await res.json();
        setSampleData(data);
      }
    } catch (e: any) {
      setSampleData(null);
      setSampleError(e.message || 'Failed to fetch sample data');
    } finally {
      setIsFetchingSample(false);
    }
  };

  const handleFileUpload = async (file: File | null) => {
    if (!file) return;
    setUploading(true);
    const formData = new FormData();
    formData.append('file', file);
    try {
      const res = await apiFetch(`${API_BASE}/sources/upload`, {
        method: 'POST',
        body: formData,
      });
      if (res.ok) {
        const data = await res.json();
        updateConfig('file_path', data.path);
      }
    } catch (e) {
      console.error(e);
    } finally {
      setUploading(false);
    }
  };

  const fetchDatabases = async () => {
    setIsFetchingDBs(true);
    try {
      const res = await apiFetch(`${API_BASE}/sources/discover/databases`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(source),
      });
      if (res.ok) {
        const dbs = await res.json();
        setDiscoveredDatabases(dbs || []);
      }
    } catch (e) {
      console.error(e);
    } finally {
      setIsFetchingDBs(false);
    }
  };

  const fetchTables = async (dbName?: string) => {
    setIsFetchingTables(true);
    try {
      const s = { 
        ...source, 
        config: { 
          ...source.config, 
          dbname: dbName || (source as any).config?.dbname || (source as any).config?.path 
        } 
      };
      const res = await apiFetch(`${API_BASE}/sources/discover/tables`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(s),
      });
      if (res.ok) {
        const tables = await res.json();
        setDiscoveredTables(tables || []);
      }
    } catch (e) {
      console.error(e);
    } finally {
      setIsFetchingTables(false);
    }
  };

  const lastInitialDataId = useRef<string | null>(null);

  useEffect(() => {
    if (initialData) {
      if (lastInitialDataId.current !== (initialData.id || 'new')) {
        const newValues = {
          ...source,
          ...initialData,
          config: {
            ...(source.config || {}),
            ...(initialData.config || {}),
            reconnect_intervals: initialData.config?.reconnect_intervals || initialData.config?.reconnect_interval || source.config?.reconnect_intervals || '30s',
          }
        };
        form.reset(newValues);
        lastInitialDataId.current = initialData.id || 'new';
      }
      if (initialData.sample) {
        try {
          setSampleData(JSON.parse(initialData.sample));
        } catch (e) {
          console.error("Failed to parse sample data", e);
        }
      }
    }
  }, [initialData, form, source]);

  useEffect(() => {
    if (embedded) {
      if (vhost && form.getFieldValue('vhost') !== vhost) {
        form.setFieldValue('vhost', vhost);
      }
      if (workerID && form.getFieldValue('worker_id') !== workerID) {
        form.setFieldValue('worker_id', workerID);
      }
    }
  }, [embedded, vhost, workerID, form]);

  const testMutation = useMutation({
    mutationFn: async (s: any) => {
      const cleanConfig = Object.fromEntries(
        Object.entries(s.config).filter(([_, v]) => v !== '')
      );
      const res = await apiFetch(`${API_BASE}/sources/test`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ ...s, config: cleanConfig }),
      });
      return res.json();
    },
    onSuccess: (_, variables) => {
      setTestResult({ status: 'ok', message: 'Connection successful!' });
      fetchSample(variables);
    },
    onError: (e: any) => {
      const data = e?.data;
      if (e?.status === 409 && data?.code === 'CDC_REUSE_PROMPT') {
        setCdcReusePrompt({ slot: data.slot, publication: data.publication });
        return;
      }
      setTestResult(null);
    }
  });

  const submitMutation = useMutation({
    mutationFn: async (s: any) => {
      const cleanConfig = Object.fromEntries(
        Object.entries(s.config).filter(([_, v]) => v !== '')
      );
      
      const res = await apiFetch(`${API_BASE}/sources${isEditing && initialData?.id ? `/${initialData.id}` : ''}`, {
        method: isEditing ? 'PUT' : 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ 
          ...s, 
          config: cleanConfig,
          sample: sampleData ? JSON.stringify(sampleData) : s.sample 
        }),
      });
      return res.json();
    },
    onSuccess: (data) => {
      if (embedded && onSave) {
        onSave(data);
      } else {
        navigate({ to: '/sources' });
      }
    },
    onError: () => {
        setTestResult(null);
    }
  });

  const handleSourceChange = (updates: any) => {
    const typeChanged = updates.type && updates.type !== source.type;
    Object.entries(updates).forEach(([key, value]) => {
      form.setFieldValue(key as any, value);
    });
    setTestResult(null);
    setSampleData(null);
    if (typeChanged) {
      setTestInput('');
      setSelectedSampleTable('');
    }
  };

  const updateConfig = (key: string, value: any) => {
    form.setFieldValue(`config.${key}` as any, value);
    setTestResult(null);
    setSampleData(null);
  };

  return {
    form,
    source,
    testResult,
    setTestResult,
    discoveredDatabases,
    discoveredTables,
    isFetchingDBs,
    isFetchingTables,
    uploading,
    sampleData,
    setSampleData,
    isFetchingSample,
    sampleError,
    setSampleError,
    testInput,
    setTestInput,
    selectedSampleTable,
    setSelectedSampleTable,
    showSetup,
    setShowSetup,
    formPreviewOpened,
    setFormPreviewOpened,
    cdcReusePrompt,
    setCdcReusePrompt,
    selectedSnapshotTables,
    setSelectedSnapshotTables,
    snapshotModalOpened,
    setSnapshotModalOpened,
    referencingWorkflows,
    isLoadingRefWf,
    referencingError,
    hasActiveReferencingWorkflow,
    snapshotMutation,
    testMutation,
    submitMutation,
    isCDC,
    isDatabaseSource,
    fetchSample,
    handleFileUpload,
    fetchDatabases,
    fetchTables,
    handleSourceChange,
    updateConfig
  };
}
