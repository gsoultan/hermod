import { useState, useEffect } from 'react';
import {
  Container, Title, Text, Paper, Group, Button, Table, ActionIcon,
  TextInput, JsonInput, Select, Modal, Stack, Badge, Tooltip,
  Loader, Alert, Menu, rem
} from '@mantine/core';import { apiFetch } from '../api';
import { formatDateTime } from '../utils/dateUtils';
import { notifications } from '@mantine/notifications';import { IconAlertCircle, IconBraces, IconCode, IconDotsVertical, IconDownload, IconHistory, IconPlus, IconTrash } from '@tabler/icons-react';
export function SchemasPage() {
  const [schemas, setSchemas] = useState<any[]>([]);
  const [loading, setLoading] = useState(true);
  const [modalOpened, setModalOpened] = useState(false);
  const [saving, setSaving] = useState(false);
  
  // Form state
  const [name, setName] = useState('');
  const [type, setType] = useState('json');
  const [content, setContent] = useState('{\n  "type": "object",\n  "properties": {}\n}');

  const fetchSchemas = async () => {
    setLoading(true);
    try {
      const res = await apiFetch('/api/schemas');
      if (res.ok) {
        const data = await res.json();
        setSchemas(data);
      }
    } catch (err) {
      console.error('Failed to fetch schemas', err);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchSchemas();
  }, []);

  const handleRegister = async () => {
    if (!name || !content) {
      notifications.show({ title: 'Error', message: 'Name and content are required', color: 'red' });
      return;
    }

    setSaving(true);
    try {
      const res = await apiFetch('/api/schemas', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name, type, content })
      });

      if (res.ok) {
        notifications.show({ title: 'Success', message: 'Schema registered successfully', color: 'green' });
        setModalOpened(false);
        fetchSchemas();
        setName('');
        setContent('{\n  "type": "object",\n  "properties": {}\n}');
      } else {
        const err = await res.json();
        notifications.show({ title: 'Error', message: err.error || 'Failed to register schema', color: 'red' });
      }
    } catch (err: any) {
      notifications.show({ title: 'Error', message: err.message, color: 'red' });
    } finally {
      setSaving(false);
    }
  };

  const rows = (Array.isArray(schemas) ? schemas : []).map((schema) => (
    <Table.Tr key={schema.id}>
      <Table.Td>
        <Group gap="sm">
          <IconBraces size="1.2rem" color="var(--mantine-color-indigo-6)" />
          <div>
            <Text size="sm" fw={500}>{schema.name}</Text>
            <Text size="xs" c="dimmed">ID: {schema.id}</Text>
          </div>
        </Group>
      </Table.Td>
      <Table.Td>
        <Badge variant="light" color={schema.type === 'json' ? 'blue' : 'orange'}>
          {schema.type.toUpperCase()}
        </Badge>
      </Table.Td>
      <Table.Td>
        <Badge variant="dot" color="green">v{schema.version}</Badge>
      </Table.Td>
      <Table.Td>
        <Text size="xs">{formatDateTime(schema.created_at)}</Text>
      </Table.Td>
      <Table.Td>
        <Group gap={0} justify="flex-end">
          <Tooltip label="View Content">
            <ActionIcon variant="subtle" color="gray">
              <IconCode size="1rem" />
            </ActionIcon>
          </Tooltip>
          <Menu transitionProps={{ transition: 'pop' }} withArrow position="bottom-end" withinPortal>
            <Menu.Target>
              <ActionIcon variant="subtle" color="gray">
                <IconDotsVertical size="1rem" />
              </ActionIcon>
            </Menu.Target>
            <Menu.Dropdown>
              <Menu.Item leftSection={<IconHistory size={rem(14)} />}>Version History</Menu.Item>
              <Menu.Item leftSection={<IconDownload size={rem(14)} />}>Download</Menu.Item>
              <Menu.Divider />
              <Menu.Item color="red" leftSection={<IconTrash size={rem(14)} />}>Delete</Menu.Item>
            </Menu.Dropdown>
          </Menu>
        </Group>
      </Table.Td>
    </Table.Tr>
  ));

  return (
    <Container size="xl" py="md">
      <Group justify="space-between" mb="xl">
        <div>
          <Title order={2}>Global Schema Registry</Title>
          <Text c="dimmed" size="sm">Manage data contracts and enforce schema validation across workflows</Text>
        </div>
        <Button leftSection={<IconPlus size="1rem" />} onClick={() => setModalOpened(true)}>
          Register Schema
        </Button>
      </Group>

      {loading ? (
        <Group justify="center" py="xl"><Loader /></Group>
      ) : schemas.length === 0 ? (
        <Paper withBorder p="xl" radius="md" ta="center">
          <IconBraces size="3rem" color="var(--mantine-color-dimmed)" style={{ marginBottom: rem(10) }} />
          <Text fw={500}>No schemas registered yet</Text>
          <Text size="sm" c="dimmed" mb="md">Register your first schema to start enforcing data contracts.</Text>
          <Button variant="light" onClick={() => setModalOpened(true)}>Register Schema</Button>
        </Paper>
      ) : (
        <Paper withBorder radius="md">
          <Table verticalSpacing="sm">
            <Table.Thead>
              <Table.Tr>
                <Table.Th>Name</Table.Th>
                <Table.Th>Type</Table.Th>
                <Table.Th>Latest Version</Table.Th>
                <Table.Th>Registered At</Table.Th>
                <Table.Th />
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>{rows}</Table.Tbody>
          </Table>
        </Paper>
      )}

      <Modal
        opened={modalOpened}
        onClose={() => setModalOpened(false)}
        title="Register New Schema"
        size="lg"
      >
        <Stack>
          <TextInput
            label="Schema Name"
            placeholder="e.g. user-events"
            required
            value={name}
            onChange={(e) => setName(e.currentTarget.value)}
          />
          <Select
            label="Schema Type"
            data={[
              { value: 'json', label: 'JSON Schema' },
              { value: 'avro', label: 'Avro' },
              { value: 'protobuf', label: 'Protobuf' },
            ]}
            value={type}
            onChange={(val) => setType(val || 'json')}
          />
          <JsonInput
            label="Schema Content"
            placeholder="Paste your schema definition here"
            validationError="Invalid JSON"
            formatOnBlur
            autosize
            minRows={10}
            required
            value={content}
            onChange={setContent}
          />
          <Alert icon={<IconAlertCircle size="1rem" />} color="blue" mt="sm">
            Compatibility check will be performed automatically before registration.
          </Alert>
          <Group justify="flex-end" mt="md">
            <Button variant="subtle" onClick={() => setModalOpened(false)}>Cancel</Button>
            <Button onClick={handleRegister} loading={saving}>Register Schema</Button>
          </Group>
        </Stack>
      </Modal>
    </Container>
  );
}


