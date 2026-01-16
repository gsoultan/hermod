import React, { useState } from 'react';
import { Stepper, Button, Group, TextInput, Select, Stack, Paper, Text, Radio, Divider, Alert, MultiSelect, Accordion, Box, ActionIcon, Badge } from '@mantine/core';
import { useMutation, useSuspenseQuery } from '@tanstack/react-query';
import { apiFetch, getRoleFromToken } from '../api';
import { useVHost } from '../context/VHostContext';
import { useNavigate } from '@tanstack/react-router';
import { SourceForm } from './SourceForm';
import { SinkForm } from './SinkForm';
import { IconDatabase, IconFilter, IconPlus, IconRefresh, IconRoute, IconTrash } from '@tabler/icons-react';
import { TransformationManager } from './TransformationManager';
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

export function ConnectionWizard() {
  const navigate = useNavigate();
  const { availableVHosts } = useVHost();
  const role = getRoleFromToken();
  const [active, setActive] = useState(0);

  const [connectionData, setConnectionData] = useState<any>({
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

  const [sourceMode, setSourceMode] = useState<'existing' | 'new'>('existing');
  const [sinkMode, setSinkMode] = useState<'existing' | 'new'>('existing');
  
  const [newSource, setNewSource] = useState<any>(null);
  const [newSink, setNewSink] = useState<any>(null);

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
      if (!res.ok) throw new Error('Failed to fetch transformations');
      return res.json();
    }
  });

  const vhosts = vhostsResponse?.data || [];
  const workers = workersResponse?.data || [];
  const sources = sourcesResponse?.data || [];
  const sinks = sinksResponse?.data || [];
  const transformations = transformationsResponse?.data || [];

  const selectedSource = sourceMode === 'existing' 
    ? sources.find((s: any) => s.id === connectionData.source_id)
    : newSource;

  const availableTables = React.useMemo(() => {
    const config = selectedSource?.config;
    if (!config) return [];
    if (config.tables) return config.tables.split(',').map((t: string) => t.trim()).filter(Boolean);
    if (config.topic) return [config.topic];
    if (config.subject) return [config.subject];
    if (config.stream) return [config.stream];
    if (config.queue) return [config.queue];
    return [];
  }, [selectedSource]);

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

  const filteredSources = sources?.filter((s: any) => 
    !s.vhost || s.vhost === connectionData.vhost || connectionData.vhost === ''
  );

  const filteredSinks = sinks?.filter((s: any) => 
    !s.vhost || s.vhost === connectionData.vhost || connectionData.vhost === ''
  );

  const createConnectionMutation = useMutation({
    mutationFn: async (_: any) => {
      let sourceId = connectionData.source_id;
      let sinkId = connectionData.sink_id;

      // 1. Create Source if new
      if (sourceMode === 'new' && newSource) {
        const res = await apiFetch(`${API_BASE}/sources`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ ...newSource, vhost: connectionData.vhost }),
        });
        if (!res.ok) {
           const err = await res.json();
           throw new Error(`Failed to create source: ${err.error}`);
        }
        const savedSource = await res.json();
        sourceId = savedSource.id;
      }

      // 2. Create Sink if new
      if (sinkMode === 'new' && newSink) {
        const res = await apiFetch(`${API_BASE}/sinks`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ ...newSink, vhost: connectionData.vhost }),
        });
        if (!res.ok) {
           const err = await res.json();
           throw new Error(`Failed to create sink: ${err.error}`);
        }
        const savedSink = await res.json();
        sinkId = savedSink.id;
      }

      // 3. Create Connection
      const res = await apiFetch(`${API_BASE}/connections`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          name: connectionData.name,
          vhost: connectionData.vhost,
          source_id: sourceId,
          sink_ids: sinkId ? [sinkId] : connectionData.sink_ids,
          worker_id: connectionData.worker_id,
          transformation_ids: connectionData.transformation_ids,
          transformations: connectionData.transformations,
          transformation_groups: connectionData.transformation_groups.map((tg: any) => ({
             ...tg,
             sink_ids: tg.sink_ids.map((sid: string) => sid === 'new_sink_placeholder' ? sinkId : sid)
          })),
        }),
      });
      if (!res.ok) {
        const err = await res.json();
        throw new Error(`Failed to create connection: ${err.error}`);
      }
      return res.json();
    },
    onSuccess: () => {
      navigate({ to: '/connections' });
    }
  });

  const nextStep = () => {
    if (active === 1 && sourceMode === 'existing') {
       // Check if there are any specific transformations to show in a separate step?
       // The requirement says "next step data transform", currently they are inside SourceForm/SinkForm
       // Let's add an explicit step for transformations if needed or just use what's there.
    }
    setActive((current) => (current < 4 ? current + 1 : current));
  };
  const prevStep = () => setActive((current) => (current > 0 ? current - 1 : current));

  return (
    <Stack>
      <Stepper active={active} onStepClick={setActive} allowNextStepsSelect={false}>
        <Stepper.Step label="General" description="Basic info">
          <Stack mt="md">
            <TextInput 
              label="Connection Name" 
              placeholder="My Connection" 
              value={connectionData.name}
              onChange={(e) => setConnectionData({ ...connectionData, name: e.target.value })}
              required
            />
            <Select 
              label="VHost" 
              placeholder="Select VHost" 
              data={availableVHostsList}
              value={connectionData.vhost}
              onChange={(val) => setConnectionData({ ...connectionData, vhost: val || '' })}
              required
            />
            <Select 
              label="Worker (Optional)" 
              placeholder="Assign to a specific worker" 
              data={(workers || []).map((w: any) => ({ value: w.id, label: w.name || w.id }))}
              value={connectionData.worker_id}
              onChange={(val) => setConnectionData({ ...connectionData, worker_id: val || '' })}
              clearable
            />
          </Stack>
        </Stepper.Step>

        <Stepper.Step label="Source" description="Choose data source">
          <Stack mt="md">
            <Radio.Group
              label="Source Selection"
              value={sourceMode}
              onChange={(val: any) => setSourceMode(val)}
            >
              <Group mt="xs">
                <Radio value="existing" label="Existing Source" />
                <Radio value="new" label="Create New Source" />
              </Group>
            </Radio.Group>

            {sourceMode === 'existing' ? (
              <Select 
                label="Pick Source" 
                placeholder="Select an existing source"
                data={filteredSources?.map((s: any) => ({ value: s.id, label: s.name }))}
                value={connectionData.source_id}
                onChange={(val) => setConnectionData({ ...connectionData, source_id: val || '' })}
                required
              />
            ) : (
               <Paper withBorder p="md">
                  <Text fw={500} mb="md">New Source Details</Text>
                  <SourceForm 
                    embedded 
                    initialData={{ vhost: connectionData.vhost }}
                    onSave={(data) => {
                      setNewSource(data);
                      nextStep();
                    }} 
                  />
               </Paper>
            )}
          </Stack>
        </Stepper.Step>

        <Stepper.Step label="Sink" description="Choose destination">
           <Stack mt="md">
            <Radio.Group
              label="Sink Selection"
              value={sinkMode}
              onChange={(val: any) => setSinkMode(val)}
            >
              <Group mt="xs">
                <Radio value="existing" label="Existing Sink" />
                <Radio value="new" label="Create New Sink" />
              </Group>
            </Radio.Group>

            {sinkMode === 'existing' ? (
              <MultiSelect 
                label="Pick Sinks" 
                placeholder="Select one or more sinks"
                data={filteredSinks?.map((s: any) => ({ value: s.id, label: s.name }))}
                value={connectionData.sink_ids}
                onChange={(val) => setConnectionData({ ...connectionData, sink_ids: val })}
                required
              />
            ) : (
                <Paper withBorder p="md">
                  <Text fw={500} mb="md">New Sink Details</Text>
                  <SinkForm 
                    embedded 
                    initialData={{ vhost: connectionData.vhost }}
                    onSave={(data) => {
                      setNewSink(data);
                      nextStep();
                    }} 
                  />
                </Paper>
            )}
          </Stack>
        </Stepper.Step>

        <Stepper.Step label="Transform" description="Data transformation">
           <Stack mt="md">
              <Alert color="blue" icon={<IconFilter size="1rem" />}>
                Configure data transformations for this connection. 
                You can set global transformations that apply to all sinks, or create transformation branches for specific sinks.
              </Alert>
              
              <Paper withBorder p="md">
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

              <Paper withBorder p="md">
                <Text fw={600} mb="xs">Predefined Transformations</Text>
                <Text size="xs" c="dimmed" mb="md">Select reusable transformations to apply globally.</Text>
                <MultiSelect 
                  label="Available Transformations" 
                  placeholder="Select reusable transformations"
                  data={(transformations || []).map((t: any) => ({ value: t.id, label: t.name }))}
                  value={connectionData.transformation_ids}
                  onChange={(val) => setConnectionData({ ...connectionData, transformation_ids: val })}
                  clearable
                />
              </Paper>

              <Paper withBorder p="md">
                <Text fw={600} mb="xs">Global Transformations</Text>
                <Text size="xs" c="dimmed" mb="md">Applied to all messages before they are branched.</Text>
                <TransformationManager 
                  title="Global Transforms" 
                  transformations={connectionData.transformations} 
                  onChange={(next) => setConnectionData({ ...connectionData, transformations: next })}
                  sampleMessage={sampleMessage}
                  onSampleMessageChange={setSampleMessage}
                />
              </Paper>

              <Divider label="Transformation Branches" labelPosition="center" my="md" />

              <Group justify="space-between" mb="xs">
                  <Text size="sm" fw={500}>Define custom transformation flows for groups of sinks.</Text>
                  <Button 
                    size="xs" 
                    variant="outline"
                    leftSection={<IconPlus size="1rem" />}
                    onClick={() => {
                        const nextGroups = [...(connectionData.transformation_groups || [])];
                        nextGroups.push({ name: `Branch ${nextGroups.length + 1}`, sink_ids: [], transformations: [] });
                        setConnectionData({ ...connectionData, transformation_groups: nextGroups });
                    }}
                  >
                    Add Branch
                  </Button>
              </Group>

              <Accordion variant="separated">
                {(connectionData.transformation_groups || []).map((group: any, index: number) => {
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
                                    const nextGroups = connectionData.transformation_groups.filter((_: any, i: number) => i !== index);
                                    setConnectionData({ ...connectionData, transformation_groups: nextGroups });
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
                                    const nextGroups = [...connectionData.transformation_groups];
                                    nextGroups[index] = { ...group, name: e.target.value };
                                    setConnectionData({ ...connectionData, transformation_groups: nextGroups });
                                }}
                            />
                            <MultiSelect 
                                label="Target Sinks"
                                description="Messages in this branch will be sent to these sinks."
                                data={(sinkMode === 'existing' ? connectionData.sink_ids : ['new_sink_placeholder']).map((id: string) => {
                                    const s = sinks.find((sink: any) => sink.id === id);
                                    return { value: id, label: id === 'new_sink_placeholder' ? (newSink?.name || 'New Sink') : (s?.name || id) };
                                })}
                                value={group.sink_ids}
                                onChange={(val) => {
                                    const nextGroups = [...connectionData.transformation_groups];
                                    nextGroups[index] = { ...group, sink_ids: val };
                                    setConnectionData({ ...connectionData, transformation_groups: nextGroups });
                                }}
                            />
                            <TransformationManager
                                title="Branch Transformations"
                                transformations={group.transformations}
                                onChange={(next) => {
                                    const nextGroups = [...connectionData.transformation_groups];
                                    nextGroups[index] = { ...group, transformations: next };
                                    setConnectionData({ ...connectionData, transformation_groups: nextGroups });
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
        </Stepper.Step>

        <Stepper.Completed>
          <Stack mt="md">
             <Alert color="blue">
                All steps completed. Review your configuration and click "Create Connection".
             </Alert>
             <Paper withBorder p="md">
                <Text fw={700}>Connection: {connectionData.name}</Text>
                <Text>VHost: {connectionData.vhost}</Text>
                <Divider my="sm" />
                <Text fw={500}>Source: {sourceMode === 'existing' ? filteredSources?.find((s:any)=>s.id===connectionData.source_id)?.name : `New (${newSource?.name})`}</Text>
                <Text fw={500} mt="xs">Global Transformations: {connectionData.transformations.length} steps</Text>
                <Divider my="sm" variant="dotted" />
                <Text fw={600} size="sm" mb="xs">Transformation Branches:</Text>
                {connectionData.transformation_groups.length === 0 ? (
                    <Text size="xs" c="dimmed">No custom branches defined. All sinks will receive messages with global transformations.</Text>
                ) : connectionData.transformation_groups.map((tg: any, idx: number) => {
                   return (
                     <Box key={idx} mb="xs">
                        <Group gap="xs">
                            <IconRoute size="0.8rem" />
                            <Text size="sm" fw={500}>{tg.name || `Branch ${idx + 1}`}</Text>
                            <Badge size="xs" variant="outline">{tg.transformations.length} transforms</Badge>
                        </Group>
                        <Group gap={4} ml="lg" mt={2}>
                            {tg.sink_ids.map((sid: string) => (
                                <Badge key={sid} size="xs" color="gray" variant="dot">
                                    {sid === 'new_sink_placeholder' ? (newSink?.name || 'New Sink') : (filteredSinks?.find((s:any)=>s.id===sid)?.name || sid)}
                                </Badge>
                            ))}
                        </Group>
                     </Box>
                   )
                })}
             </Paper>
          </Stack>
        </Stepper.Completed>
      </Stepper>

      <Group justify="flex-end" mt="xl">
        {active !== 0 && (
          <Button variant="default" onClick={prevStep}>
            Back
          </Button>
        )}
        {active < 4 ? (
          <Button onClick={nextStep} disabled={
            (active === 0 && (!connectionData.name || !connectionData.vhost)) ||
            (active === 1 && sourceMode === 'existing' && !connectionData.source_id) ||
            (active === 1 && sourceMode === 'new' && !newSource) ||
            (active === 2 && sinkMode === 'existing' && (!connectionData.sink_ids || connectionData.sink_ids.length === 0)) ||
            (active === 2 && sinkMode === 'new' && !newSink)
          }>
            Next step
          </Button>
        ) : (
          <Button 
            onClick={() => createConnectionMutation.mutate({})} 
            loading={createConnectionMutation.isPending}
          >
            Create Connection
          </Button>
        )}
      </Group>

      {createConnectionMutation.isError && (
        <Alert color="red" mt="md">
          {createConnectionMutation.error.message}
        </Alert>
      )}
    </Stack>
  );
}
