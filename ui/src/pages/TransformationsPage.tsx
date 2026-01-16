import { useState } from 'react';
import { Container, Title, Button, Group, Table, ActionIcon, Text, Badge, Paper, Stack, TextInput, Pagination } from '@mantine/core';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { IconPlus, IconTrash, IconEdit, IconSearch, IconArrowsDiff, IconFilter, IconWand, IconWorld, IconDatabase, IconRoute } from '@tabler/icons-react';
import { Link } from '@tanstack/react-router';
import { apiFetch } from '../api';

const API_BASE = '/api';

export default function TransformationsPage() {
  const queryClient = useQueryClient();
  const [search, setSearch] = useState('');
  const [activePage, setPage] = useState(1);
  const itemsPerPage = 30;

  const { data: transformationsResponse, isLoading } = useQuery<any>({
    queryKey: ['transformations', activePage, search],
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/transformations?page=${activePage}&limit=${itemsPerPage}&search=${search}`);
      return res.json();
    }
  });

  const transformations = transformationsResponse?.data || [];
  const totalItems = transformationsResponse?.total || 0;

  const deleteMutation = useMutation({
    mutationFn: async (id: string) => {
      await apiFetch(`${API_BASE}/transformations/${id}`, { method: 'DELETE' });
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['transformations'] });
    }
  });

  const totalPages = Math.ceil(totalItems / itemsPerPage);

  const getTypeIcon = (type: string) => {
    switch (type) {
      case 'filter_operation':
      case 'filter_data': return <IconFilter size="1.2rem" color="red" />;
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
              onChange={(e) => {
                setSearch(e.target.value);
                setPage(1);
              }}
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
                ) : transformations?.length === 0 ? (
                  <Table.Tr><Table.Td colSpan={4}><Text ta="center" py="xl" c="dimmed">{search ? 'No transformations match your search' : 'No transformations found'}</Text></Table.Td></Table.Tr>
                ) : transformations?.map((t: any) => (
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
                        t.type === 'http' ? 'teal' : 
                        (t.type === 'filter_data' || t.type === 'filter_operation') ? 'red' : 'gray'
                      }>
                        {t.type === 'filter_data' ? 'filter' : t.type.replace('_', ' ')}
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
            {totalPages > 1 && (
              <Group justify="center" p="md" style={{ borderTop: '1px solid var(--mantine-color-gray-1)' }}>
                <Pagination total={totalPages} value={activePage} onChange={setPage} radius="md" />
              </Group>
            )}
          </Stack>
        </Paper>
      </Stack>
    </Container>
  );
}
