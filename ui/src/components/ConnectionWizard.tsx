import { useState } from 'react';
import { Stepper, Button, Group, TextInput, Select, Stack, Paper, Text, Radio, Divider, Alert, MultiSelect } from '@mantine/core';
import { useMutation, useSuspenseQuery } from '@tanstack/react-query';
import { apiFetch, getRoleFromToken } from '../api';
import { useVHost } from '../context/VHostContext';
import { useNavigate } from '@tanstack/react-router';
import { SourceForm } from './SourceForm';
import { SinkForm } from './SinkForm';
import { IconFilter } from '@tabler/icons-react';
import { TransformationManager } from './TransformationManager';

const API_BASE = '/api';

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
    transformations: []
  });

  const [sourceMode, setSourceMode] = useState<'existing' | 'new'>('existing');
  const [sinkMode, setSinkMode] = useState<'existing' | 'new'>('existing');
  
  const [newSource, setNewSource] = useState<any>(null);
  const [newSink, setNewSink] = useState<any>(null);


  const { data: vhosts } = useSuspenseQuery<any[]>({
    queryKey: ['vhosts'],
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/vhosts`);
      if (res.ok) return res.json();
      return [];
    }
  });

  const { data: sources } = useSuspenseQuery({
    queryKey: ['sources'],
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/sources`);
      if (!res.ok) throw new Error('Failed to fetch sources');
      return res.json();
    }
  });

  const { data: sinks } = useSuspenseQuery({
    queryKey: ['sinks'],
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/sinks`);
      if (!res.ok) throw new Error('Failed to fetch sinks');
      return res.json();
    }
  });

  const availableVHostsList = role === 'Administrator' 
    ? (vhosts || []).map(v => v.name)
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
          transformations: connectionData.transformations,
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

        <Stepper.Step label="Transform" description="Data transformation">
           <Stack mt="md">
              <Alert color="blue" icon={<IconFilter size="1rem" />}>
                Configure data transformations for this connection. 
                Transformations apply to all incoming data from the source before being sent to sinks.
              </Alert>
              <TransformationManager 
                title="Transformations" 
                transformations={connectionData.transformations} 
                onChange={(next) => setConnectionData({ ...connectionData, transformations: next })}
              />
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
                <Text fw={500}>Sinks: {sinkMode === 'existing' ? connectionData.sink_ids.map((id:string) => filteredSinks?.find((s:any)=>s.id===id)?.name).join(', ') : `New (${newSink?.name})`}</Text>
                <Text size="sm" mt="xs">Transformations: {connectionData.transformations.length} steps</Text>
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
            (active === 3 && sinkMode === 'existing' && (!connectionData.sink_ids || connectionData.sink_ids.length === 0)) ||
            (active === 3 && sinkMode === 'new' && !newSink)
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
