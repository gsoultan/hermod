import { useState, useEffect } from 'react';
import { Button, Group, TextInput, Stack, Textarea, NumberInput, Alert, Text, Code, Paper } from '@mantine/core';
import { useMutation } from '@tanstack/react-query';
import { apiFetch } from '../api';
import { useNavigate } from '@tanstack/react-router';
import { IconInfoCircle, IconTerminal } from '@tabler/icons-react';

interface Worker {
  id: string;
  name: string;
  host: string;
  port: number;
  description: string;
  token?: string;
}

interface WorkerFormProps {
  initialData?: Worker;
  isEditing?: boolean;
}

export function WorkerForm({ initialData, isEditing = false }: WorkerFormProps) {
  const navigate = useNavigate();
  const [createdWorker, setCreatedWorker] = useState<Worker | null>(null);
  const [worker, setWorker] = useState<Worker>({
    id: '',
    name: '',
    host: '',
    port: 8080,
    description: ''
  });

  useEffect(() => {
    if (initialData) {
      setWorker(initialData);
    } else if (!isEditing) {
      // Generate a random ID for new workers
      setWorker(prev => ({ ...prev, id: `worker-${Math.random().toString(36).substring(2, 9)}` }));
    }
  }, [initialData, isEditing]);

  const submitMutation = useMutation({
    mutationFn: async (workerData: Worker) => {
      // Use ID as name if name is empty
      const dataToSubmit = { ...workerData, name: workerData.name || workerData.id };
      const res = await apiFetch(`/api/workers${isEditing ? `/${initialData?.id}` : ''}`, {
        method: isEditing ? 'PUT' : 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(dataToSubmit)
      });
      if (!res.ok) {
        const data = await res.json();
        throw new Error(data.error || `Failed to ${isEditing ? 'update' : 'create'} worker`);
      }
      return res.json();
    },
    onSuccess: (data) => {
      if (isEditing) {
        navigate({ to: '/workers' });
      } else {
        setCreatedWorker(data);
      }
    }
  });

  if (createdWorker) {
    return (
      <Stack gap="md">
        <Alert icon={<IconInfoCircle size="1rem" />} title="Worker Registered Successfully" color="green" radius="md">
          The worker has been registered. Use the command below to install and run the worker on your server.
        </Alert>

        <Paper withBorder p="md" radius="md" bg="gray.0">
          <Stack gap="xs">
            <Group gap="xs">
              <IconTerminal size="1rem" />
              <Text fw={700}>Installation Command:</Text>
            </Group>
            <Text size="sm">
              Copy and run this command on your server:
            </Text>
            <Code block style={{ padding: '15px', fontSize: '14px', position: 'relative' }}>
              {`hermod --mode=worker --worker-guid="${createdWorker.id}" --worker-token="${createdWorker.token}" --platform-url="${window.location.origin}"`}
              <Button 
                size="compact-xs" 
                variant="light" 
                style={{ position: 'absolute', top: '5px', right: '5px' }}
                onClick={() => {
                  navigator.clipboard.writeText(`hermod --mode=worker --worker-guid="${createdWorker.id}" --worker-token="${createdWorker.token}" --platform-url="${window.location.origin}"`);
                }}
              >
                Copy
              </Button>
            </Code>
            <Text size="xs" c="dimmed mt-sm">
              Keep this token secure. It is required for the worker to authenticate with the platform.
            </Text>
          </Stack>
        </Paper>

        <Group justify="flex-end" mt="xl">
          <Button onClick={() => navigate({ to: '/workers' })}>Back to Workers</Button>
        </Group>
      </Stack>
    );
  }

  return (
    <Stack gap="md">
      {!isEditing && (
        <Alert icon={<IconInfoCircle size="1rem" />} title="Worker Registration" color="blue" radius="md">
          Registering a worker allows you to pin specific tasks to it. After registering, 
          you must start the worker application on your server using the ID provided below.
        </Alert>
      )}

      <TextInput
        label="Worker ID"
        placeholder="e.g. worker-1"
        required
        disabled
        value={worker.id}
        description="This is the unique identifier for this worker, generated automatically."
      />

      {worker.id && (
        <Paper withBorder p="sm" radius="md" bg="gray.0">
          <Stack gap="xs">
            <Group gap="xs">
              <IconTerminal size="1rem" />
              <Text size="xs" fw={700}>How to install and run this worker:</Text>
            </Group>
            <Text size="xs">
              1. Download the Hermod binary to your server.
            </Text>
            <Text size="xs">
              2. Run the following command:
            </Text>
            <Code block>{`hermod --mode=worker --worker-guid="${worker.id}" --platform-url="${window.location.origin}"`}</Code>
            <Text size="xs" c="dimmed italic">
              Note: The worker will connect to the platform API to fetch its configuration.
            </Text>
          </Stack>
        </Paper>
      )}

      <Group grow>
        <TextInput
          label="Host"
          placeholder="e.g. localhost, 192.168.1.10"
          value={worker.host}
          onChange={(e) => setWorker({ ...worker, host: e.currentTarget.value })}
        />
        <NumberInput
          label="Port"
          placeholder="8080"
          value={worker.port}
          onChange={(val) => setWorker({ ...worker, port: Number(val) })}
        />
      </Group>
      <Textarea
        label="Description"
        placeholder="Optional description of this worker"
        value={worker.description}
        onChange={(e) => setWorker({ ...worker, description: e.currentTarget.value })}
      />
      <Group justify="flex-end" mt="xl">
        <Button variant="outline" onClick={() => navigate({ to: '/workers' })}>Cancel</Button>
        <Button onClick={() => submitMutation.mutate(worker)} loading={submitMutation.isPending}>
          {isEditing ? "Save Changes" : "Register Worker"}
        </Button>
      </Group>
    </Stack>
  );
}
