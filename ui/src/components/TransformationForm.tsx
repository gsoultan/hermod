import { useState, useEffect } from 'react';
import { Button, Group, TextInput, Select, Stack, Paper, Text, Box, JsonInput, Code, Alert, Modal, Tabs, Loader, Tooltip } from '@mantine/core';
import { useMutation, useQuery } from '@tanstack/react-query';
import { apiFetch } from '../api';
import { useNavigate } from '@tanstack/react-router';
import { TransformationManager } from './TransformationManager';
import { IconRoute, IconSettings, IconPlayerPlay, IconAlertCircle, IconInfoCircle, IconDatabase, IconWorld, IconTableImport, IconDownload, IconCopy, IconBraces } from '@tabler/icons-react';
import { useDisclosure } from '@mantine/hooks';
import { notifications } from '@mantine/notifications';

const API_BASE = '/api';

interface TransformationFormProps {
  initialData?: any;
  isEditing?: boolean;
}

export function TransformationForm({ initialData, isEditing = false }: TransformationFormProps) {
  const navigate = useNavigate();
  const [trans, setTrans] = useState<any>({ 
    name: '', 
    type: 'pipeline', 
    config: {},
    steps: []
  });

  const [testInput, setTestInput] = useState(JSON.stringify({
    id: "msg_1",
    operation: "create",
    table: "users",
    schema: "public",
    before: "",
    after: JSON.stringify({ id: 1, name: "John Doe", email: "john@example.com" }),
    metadata: {}
  }, null, 2));
  const [testResult, setTestResult] = useState<any>(null);
  const [opened, { open, close }] = useDisclosure(false);
  const [importSourceId, setImportSourceId] = useState<string | null>(null);
  const [importTable, setImportTable] = useState<string | null>(null);
  const [importUrl, setImportUrl] = useState('');

  const { data: sourcesResponse } = useQuery<any>({
    queryKey: ['sources'],
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/sources`);
      return res.json();
    },
    enabled: opened
  });

  const selectedSource = sourcesResponse?.data?.find((s: any) => s.id === importSourceId);

  const { data: tables, isLoading: isLoadingTables } = useQuery<string[]>({
    queryKey: ['sources', importSourceId, 'tables'],
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/sources/discover/tables`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(selectedSource)
      });
      return res.json();
    },
    enabled: !!selectedSource && (['postgres', 'mysql', 'sqlite', 'mssql'].includes(selectedSource.type))
  });

  const importDbMutation = useMutation({
    mutationFn: async () => {
      const res = await apiFetch(`${API_BASE}/sources/sample`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ source: selectedSource, table: importTable })
      });
      if (!res.ok) throw new Error(await res.text());
      return res.json();
    },
    onSuccess: (data) => {
      setTestInput(JSON.stringify(data, null, 2));
      close();
      notifications.show({ title: 'Import Successful', message: 'Sample record imported from database.', color: 'green' });
    },
    onError: (err) => {
      notifications.show({ title: 'Import Failed', message: err.message, color: 'red' });
    }
  });

  const importApiMutation = useMutation({
    mutationFn: async () => {
      const res = await apiFetch(`${API_BASE}/proxy/fetch`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ url: importUrl })
      });
      if (!res.ok) throw new Error(await res.text());
      return res.json();
    },
    onSuccess: (data) => {
      setTestInput(JSON.stringify(data, null, 2));
      close();
      notifications.show({ title: 'Import Successful', message: 'Sample data imported from API.', color: 'green' });
    },
    onError: (err) => {
      notifications.show({ title: 'Import Failed', message: err.message, color: 'red' });
    }
  });

  const testMutation = useMutation({
    mutationFn: async () => {
      let msg;
      try {
        msg = JSON.parse(testInput);
      } catch (e) {
        throw new Error('Invalid JSON in Input Message');
      }
      
      const res = await apiFetch(`${API_BASE}/transformations/test`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          transformation: trans,
          message: msg
        }),
      });
      if (!res.ok) throw new Error(await res.text());
      return res.json();
    },
    onSuccess: (data) => {
      setTestResult(data);
    },
    onError: (err) => {
      setTestResult({ error: err.message });
    }
  });

  useEffect(() => {
    if (initialData) {
      setTrans({
        ...initialData,
        config: initialData.config || {},
        steps: initialData.steps || [],
      });
    }
  }, [initialData]);

  const submitMutation = useMutation({
    mutationFn: async (t: any) => {
      const res = await apiFetch(`${API_BASE}/transformations${isEditing ? `/${initialData.id}` : ''}`, {
        method: isEditing ? 'PUT' : 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(t),
      });
      return res.json();
    },
    onSuccess: () => {
      navigate({ to: '/transformations' });
    }
  });

  const transformerTypes = [
    { value: 'pipeline', label: 'Pipeline (Multi-step)' },
    { value: 'filter_data', label: 'Message Filter' },
    { value: 'http', label: 'HTTP Enrichment' },
    { value: 'sql', label: 'SQL Enrichment' },
    { value: 'mapping', label: 'Field Mapping' },
    { value: 'advanced', label: 'Advanced Mapping' },
    { value: 'lua', label: 'Lua Script' },
    { value: 'schema', label: 'JSON Schema' },
    { value: 'validator', label: 'Data Validator' },
  ];

  return (
    <Stack>
      <Modal opened={opened} onClose={close} title="Import Sample Message" size="lg">
        <Tabs defaultValue="db">
          <Tabs.List>
            <Tabs.Tab value="db" leftSection={<IconDatabase size="0.8rem" />}>Database Table</Tabs.Tab>
            <Tabs.Tab value="api" leftSection={<IconWorld size="0.8rem" />}>External API</Tabs.Tab>
          </Tabs.List>

          <Tabs.Panel value="db" pt="md">
            <Stack gap="md">
              <Select 
                label="Source" 
                placeholder="Pick a database source"
                data={(sourcesResponse?.data || [])
                  .filter((s: any) => ['postgres', 'mysql', 'sqlite', 'mssql'].includes(s.type))
                  .map((s: any) => ({ value: s.id, label: `${s.name} (${s.type})` }))}
                value={importSourceId}
                onChange={setImportSourceId}
              />
              <Select 
                label="Table" 
                placeholder="Select table to sample"
                data={tables?.map(t => ({ value: t, label: t })) || []}
                value={importTable}
                onChange={setImportTable}
                disabled={!importSourceId || isLoadingTables}
                rightSection={isLoadingTables ? <Loader size="xs" /> : null}
              />
              <Button 
                leftSection={<IconDownload size="1rem" />} 
                onClick={() => importDbMutation.mutate()}
                disabled={!importTable}
                loading={importDbMutation.isPending}
              >
                Fetch Sample Record
              </Button>
            </Stack>
          </Tabs.Panel>

          <Tabs.Panel value="api" pt="md">
            <Stack gap="md">
              <TextInput 
                label="API URL" 
                placeholder="https://api.example.com/data.json" 
                value={importUrl}
                onChange={(e) => setImportUrl(e.target.value)}
              />
              <Text size="xs" c="dimmed">The API should return a JSON object or array. It will be wrapped in a Hermod message structure.</Text>
              <Button 
                leftSection={<IconDownload size="1rem" />} 
                onClick={() => importApiMutation.mutate()}
                disabled={!importUrl}
                loading={importApiMutation.isPending}
              >
                Fetch API Data
              </Button>
            </Stack>
          </Tabs.Panel>
        </Tabs>
      </Modal>

      <Paper p="md" withBorder radius="md">
        <Stack gap="md">
          <Group gap="xs" mb="xs">
            <IconSettings size="1.2rem" />
            <Text fw={600}>General Settings</Text>
          </Group>
          <TextInput 
            label="Name" 
            placeholder="Data Masking Pipeline" 
            value={trans.name}
            onChange={(e) => setTrans({ ...trans, name: e.target.value })}
            required
          />
          <Select 
            label="Type" 
            placeholder="Select transformation type" 
            data={transformerTypes}
            value={trans.type}
            onChange={(val) => setTrans({ ...trans, type: val || 'pipeline', steps: val === 'pipeline' ? trans.steps : [], config: {} })}
            required
          />
        </Stack>
      </Paper>

      {trans.type === 'pipeline' ? (
        <Paper p="md" withBorder radius="md">
          <Stack gap="md">
            <Group gap="xs">
              <IconRoute size="1.2rem" />
              <Text fw={600}>Pipeline Steps</Text>
            </Group>
            <Text size="xs" c="dimmed">Define a sequence of transformations to be applied to the message.</Text>
            <TransformationManager 
              title="Steps" 
              transformations={trans.steps} 
              onChange={(next) => setTrans({ ...trans, steps: next })}
              sampleMessage={testInput}
            />
          </Stack>
        </Paper>
      ) : (
        <Paper p="md" withBorder radius="md">
          <Stack gap="md">
            <Text fw={600}>Configuration</Text>
            <Box style={{ border: '1px solid var(--mantine-color-gray-2)', borderRadius: '8px', padding: '16px' }}>
              <TransformationManager 
                title="" 
                transformations={[{ type: trans.type, config: trans.config }]} 
                sampleMessage={testInput}
                onChange={(next) => {
                  if (next.length > 0) {
                    // Only take the first one and only if it's the same type (TransformationManager might have added one)
                    const item = next.find(t => t.type === trans.type) || next[0];
                    setTrans({ ...trans, config: item.config });
                  }
                }}
              />
              <Text size="xs" c="dimmed" mt="sm">Note: You are editing a single {trans.type.replace('_', ' ')} transformer. To use multiple steps, change the Type to Pipeline.</Text>
            </Box>
          </Stack>
        </Paper>
      )}

      <Paper p="md" withBorder radius="md">
        <Stack gap="md">
          <Group justify="space-between">
            <Group gap="xs">
              <IconPlayerPlay size="1.2rem" color="var(--mantine-color-blue-filled)" />
              <Text fw={600}>Transformation Playground</Text>
            </Group>
            <Group gap="xs">
              <Tooltip label="Import from database or API">
                <Button 
                  size="xs" 
                  variant="light" 
                  color="gray"
                  leftSection={<IconTableImport size="0.8rem" />} 
                  onClick={open}
                >
                  Import Sample
                </Button>
              </Tooltip>
              <Button 
                size="xs" 
                leftSection={<IconPlayerPlay size="0.8rem" />} 
                onClick={() => testMutation.mutate()}
                loading={testMutation.isPending}
              >
                Test Transformation
              </Button>
            </Group>
          </Group>
          
          <Text size="xs" c="dimmed">Test your transformation logic with a sample message before saving.</Text>
          
          <Group grow align="flex-start">
            <Stack gap="xs">
              <Group justify="space-between" align="center">
                <Text size="xs" fw={500}>Input Message (JSON)</Text>
                <Button 
                  variant="subtle" 
                  size="compact-xs" 
                  leftSection={<IconBraces size="1rem" />}
                  onClick={() => {
                    try {
                      setTestInput(JSON.stringify(JSON.parse(testInput), null, 2));
                    } catch (e) {
                      notifications.show({ title: 'Invalid JSON', message: 'Could not format invalid JSON.', color: 'red' });
                    }
                  }}
                >
                  Format
                </Button>
              </Group>
              <JsonInput 
                size="xs" 
                styles={{ input: { fontFamily: 'monospace', height: '400px' } }}
                value={testInput}
                onChange={setTestInput}
                validationError="Invalid JSON"
                formatOnBlur
              />
            </Stack>

            <Stack gap="xs">
              <Group justify="space-between" align="center">
                <Text size="xs" fw={500}>Result</Text>
                {testResult && !testResult.error && !testResult.filtered && (
                  <Button 
                    variant="subtle" 
                    size="compact-xs" 
                    leftSection={<IconCopy size="1rem" />}
                    onClick={() => {
                      navigator.clipboard.writeText(JSON.stringify(testResult, null, 2));
                      notifications.show({ title: 'Copied', message: 'Result copied to clipboard.', color: 'green', autoClose: 2000 });
                    }}
                  >
                    Copy
                  </Button>
                )}
              </Group>
              <Box style={{ minHeight: '400px' }}>
                {testResult ? (
                testResult.error ? (
                  <Alert icon={<IconAlertCircle size="1rem" />} title="Error" color="red">
                    {testResult.error}
                  </Alert>
                ) : (
                  <Stack gap="xs">
                    {testResult.filtered ? (
                      <Stack gap="xs">
                        <Alert icon={<IconInfoCircle size="1rem" />} title="Message Dropped" color="orange">
                          The message was filtered out by your configuration. It will NOT be sent to the sink.
                        </Alert>
                        <Text size="xs" c="dimmed" fs="italic">
                          Tip: A data filter only keeps messages where the condition is TRUE.
                        </Text>
                      </Stack>
                    ) : (
                      <Code block style={{ fontSize: '10px', minHeight: '400px' }}>
                        {JSON.stringify(testResult, null, 2)}
                      </Code>
                    )}
                  </Stack>
                )
              ) : (
                <Box style={{ 
                  height: '400px', 
                  display: 'flex', 
                  alignItems: 'center', 
                  justifyContent: 'center', 
                  border: '1px dashed var(--mantine-color-gray-3)',
                  borderRadius: 'var(--mantine-radius-sm)'
                }}>
                  <Text size="xs" c="dimmed">Click "Test" to see result</Text>
                </Box>
              )}
              </Box>
            </Stack>
          </Group>
        </Stack>
      </Paper>

      <Group justify="flex-end" mt="xl">
        <Button variant="outline" onClick={() => navigate({ to: '/transformations' })}>Cancel</Button>
        <Button onClick={() => submitMutation.mutate(trans)} loading={submitMutation.isPending} disabled={!trans.name || !trans.type}>
          {isEditing ? 'Save Changes' : 'Create Transformation'}
        </Button>
      </Group>
    </Stack>
  );
}
