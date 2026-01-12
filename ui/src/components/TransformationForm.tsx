import { useState, useEffect } from 'react';
import { Button, Group, TextInput, Select, Stack, Paper, Text, Box } from '@mantine/core';
import { useMutation } from '@tanstack/react-query';
import { apiFetch } from '../api';
import { useNavigate } from '@tanstack/react-router';
import { TransformationManager } from './TransformationManager';
import { IconRoute, IconSettings } from '@tabler/icons-react';

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
    { value: 'rename_table', label: 'Rename Table' },
    { value: 'filter_operation', label: 'Filter Operation' },
    { value: 'filter_data', label: 'Filter Data' },
    { value: 'http', label: 'HTTP Enrichment' },
    { value: 'sql', label: 'SQL Enrichment' },
    { value: 'mapping', label: 'Field Mapping' },
    { value: 'advanced', label: 'Advanced Mapping' },
  ];

  return (
    <Stack>
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

      <Group justify="flex-end" mt="xl">
        <Button variant="outline" onClick={() => navigate({ to: '/transformations' })}>Cancel</Button>
        <Button onClick={() => submitMutation.mutate(trans)} loading={submitMutation.isPending} disabled={!trans.name || !trans.type}>
          {isEditing ? 'Save Changes' : 'Create Transformation'}
        </Button>
      </Group>
    </Stack>
  );
}
