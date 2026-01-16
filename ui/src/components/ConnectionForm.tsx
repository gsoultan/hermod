import { useState, useEffect } from 'react';
import { Button, Group, TextInput, Select, Stack, Divider, MultiSelect, Paper, Text, ActionIcon, Accordion, Box, Alert } from '@mantine/core';
import { useMutation, useSuspenseQuery } from '@tanstack/react-query';
import { apiFetch, getRoleFromToken } from '../api';
import { useVHost } from '../context/VHostContext';
import { useNavigate } from '@tanstack/react-router';
import { TransformationManager } from './TransformationManager';
import { IconArrowsDiff, IconPlus, IconRoute, IconTrash, IconFilter, IconDatabase, IconRefresh } from '@tabler/icons-react';
import { notifications } from '@mantine/notifications';

const API_BASE = '/api';

const DEFAULT_SAMPLE_MESSAGE = JSON.stringify({
  id: "msg_1",
  operation: "create",
  table: "users",
  schema: "public",
  before: "",
  after: JSON.stringify({ id: 1, name: "John Doe", email: "john@example.com" }),
  metadata: {}
}, null, 2);

interface ConnectionFormProps {
  initialData?: any;
  isEditing?: boolean;
}

export function ConnectionForm({ initialData, isEditing = false }: ConnectionFormProps) {
  const navigate = useNavigate();
  const { availableVHosts } = useVHost();
  const role = getRoleFromToken();
  const [conn, setConn] = useState<any>({ 
    name: '', 
    vhost: '', 
    source_id: '', 
    sink_ids: [],
    worker_id: '',
    transformation_ids: [],
    transformations: [],
    transformation_groups: []
  });

  const [sampleMessage, setSampleMessage] = useState(DEFAULT_SAMPLE_MESSAGE);
  const [isSampling, setIsSampling] = useState(false);
  const [selectedSampleTable, setSelectedSampleTable] = useState('');

  useEffect(() => {
    if (initialData) {
      let transformation_groups = initialData.transformation_groups || [];
      if (typeof transformation_groups === 'string') {
        try {
          transformation_groups = JSON.parse(transformation_groups);
        } catch (e) {
          console.error('Failed to parse transformation_groups', e);
          transformation_groups = [];
        }
      }

      // Ensure transformations in groups are also parsed if they are strings
      transformation_groups = transformation_groups.map((tg: any) => ({
        ...tg,
        transformations: (tg.transformations || []).map((t: any) => ({
          ...t,
          config: typeof t.config === 'string' ? JSON.parse(t.config) : t.config
        }))
      }));

      setConn({
        ...initialData,
        transformation_ids: initialData.transformation_ids || [],
        transformations: (initialData.transformations || []).map((t: any) => ({
          ...t,
          config: typeof t.config === 'string' ? JSON.parse(t.config) : t.config
        })),
        transformation_groups: transformation_groups,
      });
    }
  }, [initialData]);

  const { data: vhostsResponse } = useSuspenseQuery<any>({
    queryKey: ['vhosts'],
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/vhosts`);
      if (res.ok) return res.json();
      return { data: [], total: 0 };
    }
  });

  const { data: workersResponse } = useSuspenseQuery<any>({
    queryKey: ['workers'],
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/workers`);
      if (res.ok) return res.json();
      return { data: [], total: 0 };
    }
  });

  const { data: sourcesResponse } = useSuspenseQuery<any>({
    queryKey: ['sources'],
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/sources`);
      if (!res.ok) throw new Error('Failed to fetch sources');
      return res.json();
    }
  });

  const { data: sinksResponse } = useSuspenseQuery<any>({
    queryKey: ['sinks'],
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/sinks`);
      if (!res.ok) throw new Error('Failed to fetch sinks');
      return res.json();
    }
  });

  const { data: transformationsResponse } = useSuspenseQuery<any>({
    queryKey: ['transformations'],
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/transformations`);
      return res.json();
    }
  });

  const vhosts = vhostsResponse?.data || [];
  const workers = workersResponse?.data || [];
  const sources = sourcesResponse?.data || [];
  const sinks = sinksResponse?.data || [];
  const transformations = transformationsResponse?.data || [];

  const selectedSource = sources.find((s: any) => s.id === conn.source_id);

  const availableTables = (() => {
    const config = selectedSource?.config;
    if (!config) return [];
    if (config.tables) return config.tables.split(',').map((t: string) => t.trim()).filter(Boolean);
    if (config.topic) return [config.topic];
    if (config.subject) return [config.subject];
    if (config.stream) return [config.stream];
    if (config.queue) return [config.queue];
    return [];
  })();

  const fetchSample = async () => {
    if (!selectedSource || !selectedSampleTable) return;

    setIsSampling(true);
    try {
      const res = await apiFetch(`${API_BASE}/sources/sample`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          source: selectedSource,
          table: selectedSampleTable
        }),
      });

      if (res.ok) {
        const msg = await res.json();
        setSampleMessage(JSON.stringify(msg, null, 2));
        notifications.show({
          title: 'Sample Loaded',
          message: `Successfully loaded sample from table ${selectedSampleTable}`,
          color: 'green'
        });
      } else {
        const err = await res.json();
        notifications.show({
          title: 'Sample Failed',
          message: err.error || 'Failed to fetch sample from source',
          color: 'red'
        });
      }
    } catch (e: any) {
      notifications.show({
        title: 'Error',
        message: e.message,
        color: 'red'
      });
    } finally {
      setIsSampling(false);
    }
  };

  const availableVHostsList = role === 'Administrator' 
    ? (vhosts || []).map((v: any) => v.name)
    : availableVHosts;

  const availableSources = sources?.filter((s: any) => 
    !s.vhost || s.vhost === conn.vhost || conn.vhost === ''
  );

  const availableSinks = sinks?.filter((s: any) => 
    !s.vhost || s.vhost === conn.vhost || conn.vhost === ''
  );

  const submitMutation = useMutation({
    mutationFn: async (c: any) => {
      const payload = {
        ...c,
        transformations: (c.transformations || []).map((t: any) => ({
          ...t,
          config: typeof t.config === 'string' ? JSON.parse(t.config) : t.config
        })),
        transformation_groups: (c.transformation_groups || []).map((tg: any) => ({
          ...tg,
          transformations: (tg.transformations || []).map((t: any) => ({
            ...t,
            config: typeof t.config === 'string' ? JSON.parse(t.config) : t.config
          }))
        }))
      };
      const res = await apiFetch(`${API_BASE}/connections${isEditing ? `/${initialData.id}` : ''}`, {
        method: isEditing ? 'PUT' : 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      });
      if (!res.ok) {
        const errData = await res.json();
        throw new Error(errData.error || `Failed to ${isEditing ? 'update' : 'create'} connection`);
      }
      return res.json();
    },
    onSuccess: () => {
      navigate({ to: '/connections' });
    }
  });


  return (
    <Stack>
      <TextInput 
        label="Name" 
        placeholder="Production Connection" 
        value={conn.name}
        onChange={(e) => setConn({ ...conn, name: e.target.value })}
        required
      />
      <Select 
        label="VHost" 
        placeholder="Select a virtual host" 
        data={availableVHostsList}
        value={conn.vhost}
        onChange={(val) => setConn({ ...conn, vhost: val || '' })}
        required
      />
      <Select 
        label="Worker (Optional)" 
        placeholder="Assign to a specific worker" 
        data={(workers || []).map((w: any) => ({ value: w.id, label: w.name || w.id }))}
        value={conn.worker_id}
        onChange={(val) => setConn({ ...conn, worker_id: val || '' })}
        clearable
      />
      <Select 
        label="Source" 
        placeholder="Pick a source"
        data={availableSources?.map((s: any) => ({ value: s.id, label: s.name }))}
        value={conn.source_id}
        onChange={(val) => setConn({ ...conn, source_id: val || '', name: conn.name || `Conn ${sources?.find((s:any)=>s.id===val)?.name}` })}
        required
      />
      <MultiSelect 
        label="Sinks" 
        placeholder="Pick one or more sinks"
        data={availableSinks?.map((s: any) => ({ value: s.id, label: s.name }))}
        value={conn.sink_ids}
        onChange={(val) => setConn({ ...conn, sink_ids: val })}
        required
      />

      <Paper withBorder p="md" radius="md" mt="md">
        <Stack gap="sm">
          <Group gap="xs" mb="xs">
            <IconArrowsDiff size="1.2rem" />
            <Text fw={600}>Message Transformations</Text>
          </Group>

          <Paper withBorder p="md" radius="md">
            <Text fw={600} mb="xs">Sample Data Selection</Text>
            <Text size="xs" c="dimmed" mb="md">Fetch a real message from your source to help with transformation mapping.</Text>
            <Group align="flex-end">
              <Select 
                label="Select Table" 
                placeholder="Pick a table to sample"
                data={availableTables}
                value={selectedSampleTable}
                onChange={(val) => setSelectedSampleTable(val || '')}
                style={{ flex: 1 }}
                leftSection={<IconDatabase size="1rem" />}
              />
              <Button 
                onClick={fetchSample} 
                loading={isSampling} 
                disabled={!selectedSampleTable}
                variant="light"
                leftSection={<IconRefresh size="1rem" />}
              >
                Fetch Sample
              </Button>
            </Group>
          </Paper>

          <MultiSelect 
            label="Predefined Transformations" 
            placeholder="Select reusable transformations"
            data={(transformations || []).map((t: any) => ({ value: t.id, label: t.name }))}
            value={conn.transformation_ids}
            onChange={(val) => setConn({ ...conn, transformation_ids: val })}
            clearable
          />

          <Divider label="Custom Inline Pipeline" labelPosition="center" my="sm" />
          
          <TransformationManager 
            title="Inline Steps" 
            transformations={conn.transformations} 
            onChange={(next) => setConn({ ...conn, transformations: next })}
            sampleMessage={sampleMessage}
            onSampleMessageChange={setSampleMessage}
          />

          <Divider label="Transformation Branches" labelPosition="center" my="md" />

          <Alert color="blue" icon={<IconFilter size="1rem" />} variant="light">
            You can create transformation branches for specific sinks. 
            These will apply after global and inline transformations.
          </Alert>

          <Group justify="space-between" mb="xs">
            <Text size="sm" fw={500}>Define custom transformation flows for groups of sinks.</Text>
            <Button 
              size="xs" 
              variant="outline"
              leftSection={<IconPlus size="1rem" />}
              onClick={() => {
                const nextGroups = [...(conn.transformation_groups || [])];
                nextGroups.push({ name: `Branch ${nextGroups.length + 1}`, sink_ids: [], transformations: [] });
                setConn({ ...conn, transformation_groups: nextGroups });
              }}
            >
              Add Branch
            </Button>
          </Group>

          <Accordion variant="separated">
            {(conn.transformation_groups || []).map((group: any, index: number) => {
              return (
                <Accordion.Item key={index} value={`group-${index}`}>
                  <Accordion.Control icon={<IconRoute size="1rem" />}>
                    <Group justify="space-between" pr="md">
                      <Box>
                        <Text fw={500}>{group.name || `Branch ${index + 1}`}</Text>
                        <Text size="xs" c="dimmed">
                          {group.sink_ids.length} sinks · {group.transformations.length} transforms
                        </Text>
                      </Box>
                      <ActionIcon 
                        color="red" 
                        variant="subtle" 
                        onClick={(e: React.MouseEvent) => {
                          e.stopPropagation();
                          const nextGroups = conn.transformation_groups.filter((_: any, i: number) => i !== index);
                          setConn({ ...conn, transformation_groups: nextGroups });
                        }}
                      >
                        <IconTrash size="1rem" />
                      </ActionIcon>
                    </Group>
                  </Accordion.Control>
                  <Accordion.Panel>
                    <Stack gap="md">
                      <TextInput 
                        label="Branch Name" 
                        placeholder="E.g. External API, Data Warehouse"
                        value={group.name}
                        onChange={(e) => {
                          const nextGroups = [...conn.transformation_groups];
                          nextGroups[index] = { ...group, name: e.target.value };
                          setConn({ ...conn, transformation_groups: nextGroups });
                        }}
                      />
                      <MultiSelect 
                        label="Target Sinks"
                        description="Messages in this branch will be sent to these sinks."
                        data={(conn.sink_ids || []).map((id: string) => {
                          const s = sinks.find((sink: any) => sink.id === id);
                          return { value: id, label: s?.name || id };
                        })}
                        value={group.sink_ids}
                        onChange={(val) => {
                          const nextGroups = [...conn.transformation_groups];
                          nextGroups[index] = { ...group, sink_ids: val };
                          setConn({ ...conn, transformation_groups: nextGroups });
                        }}
                      />
                      <TransformationManager
                        title="Branch Transformations"
                        transformations={group.transformations}
                        onChange={(next) => {
                          const nextGroups = [...conn.transformation_groups];
                          nextGroups[index] = { ...group, transformations: next };
                          setConn({ ...conn, transformation_groups: nextGroups });
                        }}
                        sampleMessage={sampleMessage}
                        onSampleMessageChange={setSampleMessage}
                      />
                    </Stack>
                  </Accordion.Panel>
                </Accordion.Item>
              );
            })}
          </Accordion>
        </Stack>
      </Paper>

      <Group justify="flex-end" mt="xl">
        <Button variant="outline" onClick={() => navigate({ to: '/connections' })}>Cancel</Button>
        <Button onClick={() => submitMutation.mutate(conn)} loading={submitMutation.isPending} disabled={!conn.name || !conn.vhost || !conn.source_id || !conn.sink_ids || conn.sink_ids.length === 0}>
          {isEditing ? 'Save Changes' : 'Create Connection'}
        </Button>
      </Group>
    </Stack>
  );
}
