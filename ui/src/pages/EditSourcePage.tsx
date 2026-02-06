import { Title, Paper, Stack, Group, Box, Text, Center, Loader } from '@mantine/core';import { SourceForm } from '../components/SourceForm';
import { useSuspenseQuery } from '@tanstack/react-query';
import { apiFetch } from '../api';
import { useParams } from '@tanstack/react-router';import { IconDatabaseImport } from '@tabler/icons-react';
export function EditSourcePage() {
  const { sourceId } = useParams({ from: '/sources/$sourceId/edit' });

  const { data: source, isLoading } = useSuspenseQuery({
    queryKey: ['sources', sourceId],
    queryFn: async () => {
      const res = await apiFetch(`/api/sources/${sourceId}`);
      if (!res.ok) throw new Error('Failed to fetch source');
      return res.json();
    }
  });

  if (isLoading) {
    return (
      <Center h="100vh">
        <Loader size="xl" />
      </Center>
    );
  }

  return (
    <Box p="md">
      <Stack gap="lg">
        <Paper p="md" withBorder radius="md" bg="gray.0">
          <Group gap="sm">
            <IconDatabaseImport size="2rem" color="var(--mantine-color-blue-filled)" />
            <Box>
              <Title order={2} fw={800}>Edit Source: {source?.name}</Title>
              <Text size="sm" c="dimmed">Update your data source configuration.</Text>
            </Box>
          </Group>
        </Paper>

        <Paper p="xl" withBorder radius="md">
          <SourceForm initialData={source} isEditing />
        </Paper>
      </Stack>
    </Box>
  );
}


