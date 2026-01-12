import { useState } from 'react';
import { Container, Title, Button, Group, Table, ActionIcon, Text, Badge, Paper, Stack, TextInput } from '@mantine/core';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { IconPlus, IconTrash, IconEdit, IconSearch, IconArrowsDiff, IconFilter, IconTableAlias, IconWand, IconWorld, IconDatabase, IconRoute } from '@tabler/icons-react';
import { Link } from '@tanstack/react-router';
import { apiFetch } from '../api';

const API_BASE = '/api';

export default function TransformationsPage() {
  const queryClient = useQueryClient();
  const [search, setSearch] = useState('');

  const { data: transformations, isLoading } = useQuery<any[]>({
    queryKey: ['transformations'],
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/transformations`);
      return res.json();
    }
  });

  const deleteMutation = useMutation({
    mutationFn: async (id: string) => {
      await apiFetch(`${API_BASE}/transformations/${id}`, { method: 'DELETE' });
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['transformations'] });
    }
  });

  const filtered = transformations?.filter(t => 
    t.name.toLowerCase().includes(search.toLowerCase()) || 
    t.type.toLowerCase().includes(search.toLowerCase())
  );

  const getTypeIcon = (type: string) => {
    switch (type) {
      case 'rename_table': return <IconTableAlias size="1.2rem" />;
      case 'filter_operation': return <IconFilter size="1.2rem" color="orange" />;
      case 'mapping': return <IconArrowsDiff size="1.2rem" color="blue" />;
      case 'advanced': return <IconWand size="1.2rem" color="indigo" />;
      case 'http': return <IconWorld size="1.2rem" color="teal" />;
      case 'sql': return <IconDatabase size="1.2rem" color="cyan" />;
      case 'pipeline': return <IconRoute size="1.2rem" color="grape" />;
      default: return null;
    }
  };

  return (
    <Container size="xl">
      <Stack gap="lg">
        <Group justify="space-between">
          <Title order={2}>Transformations</Title>
          <Button component={Link} to="/transformations/new" leftSection={<IconPlus size="1rem" />}>
            Add Transformation
          </Button>
        </Group>

        <Paper p="md" withBorder radius="md">
          <Stack gap="md">
            <TextInput 
              placeholder="Search transformations..." 
              leftSection={<IconSearch size="1rem" />} 
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />

            <Table verticalSpacing="sm">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>Name</Table.Th>
                  <Table.Th>Type</Table.Th>
                  <Table.Th>Config Summary</Table.Th>
                  <Table.Th style={{ width: 100 }}>Actions</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {isLoading ? (
                  <Table.Tr><Table.Td colSpan={4}><Text ta="center" py="xl" c="dimmed">Loading transformations...</Text></Table.Td></Table.Tr>
                ) : filtered?.length === 0 ? (
                  <Table.Tr><Table.Td colSpan={4}><Text ta="center" py="xl" c="dimmed">No transformations found</Text></Table.Td></Table.Tr>
                ) : filtered?.map((t) => (
                  <Table.Tr key={t.id}>
                    <Table.Td>
                      <Group gap="sm">
                        {getTypeIcon(t.type)}
                        <Text fw={500}>{t.name}</Text>
                      </Group>
                    </Table.Td>
                    <Table.Td>
                      <Badge variant="light" color={
                        t.type === 'pipeline' ? 'grape' : 
                        t.type === 'advanced' ? 'indigo' : 
                        t.type === 'http' ? 'teal' : 'gray'
                      }>
                        {t.type.replace('_', ' ')}
                      </Badge>
                    </Table.Td>
                    <Table.Td>
                      <Text size="xs" c="dimmed" style={{ maxWidth: 300 }} truncate="end">
                        {t.type === 'pipeline' 
                          ? `${t.steps?.length || 0} steps` 
                          : JSON.stringify(t.config)}
                      </Text>
                    </Table.Td>
                    <Table.Td>
                      <Group gap={4} justify="flex-end">
                        <ActionIcon component={Link} to={`/transformations/${t.id}/edit`} variant="subtle" color="blue">
                          <IconEdit size="1rem" />
                        </ActionIcon>
                        <ActionIcon 
                          variant="subtle" 
                          color="red" 
                          onClick={() => {
                            if (confirm('Are you sure you want to delete this transformation?')) {
                              deleteMutation.mutate(t.id);
                            }
                          }}
                        >
                          <IconTrash size="1rem" />
                        </ActionIcon>
                      </Group>
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Stack>
        </Paper>
      </Stack>
    </Container>
  );
}
