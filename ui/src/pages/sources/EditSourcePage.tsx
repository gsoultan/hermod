import { Title, Paper, Stack, Group, Box, Text } from '@mantine/core';
import { Suspense } from 'react';
import { FormSkeleton } from '@/components/common/FormSkeleton';
import { SourceForm } from '@/components/forms/SourceForm';
import { useSuspenseQuery } from '@tanstack/react-query';
import { apiFetch } from '@/api';
import { useParams } from '@tanstack/react-router';
import { IconDatabaseImport } from '@tabler/icons-react';
export function EditSourcePage() {
  const { sourceId } = useParams({ from: '/sources/$sourceId/edit' });

  const { data: source } = useSuspenseQuery({
    queryKey: ['sources', sourceId],
    queryFn: async () => {
      const res = await apiFetch(`/api/sources/${sourceId}`);
      if (!res.ok) throw new Error('Failed to fetch source');
      return res.json();
    }
  });

  return (
    <Box p="md">
      <Stack gap="lg">
        <Paper p="md" withBorder radius="md" bg="var(--mantine-color-body)">
          <Group gap="sm">
            <IconDatabaseImport size="2rem" color="var(--mantine-color-blue-filled)" />
            <Box>
              <Title order={2} fw={800}>Edit Source: {source?.name}</Title>
              <Text size="sm" c="dimmed">Update your data source configuration.</Text>
            </Box>
          </Group>
        </Paper>

        <Paper p="xl" withBorder radius="md">
          <Suspense fallback={<FormSkeleton />}>
            <SourceForm initialData={source} isEditing />
          </Suspense>
        </Paper>
      </Stack>
    </Box>
  );
}


