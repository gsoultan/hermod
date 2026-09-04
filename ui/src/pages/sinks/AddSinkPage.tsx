import { Title, Paper, Stack, Group, Box, Text } from '@mantine/core';
import { Suspense } from 'react';
import { FormSkeleton } from '@/components/common/FormSkeleton';
import { SinkForm } from '@/components/forms/SinkForm';
import { IconExternalLink } from '@tabler/icons-react';
export function AddSinkPage() {
  return (
    <Box p="md">
      <Stack gap="lg">
        <Paper p="md" withBorder radius="md" bg="var(--mantine-color-body)">
          <Group gap="sm">
            <IconExternalLink size="2rem" color="var(--mantine-color-blue-filled)" />
            <Box>
              <Title order={2} fw={800}>Add New Sink</Title>
              <Text size="sm" c="dimmed">Configure a new destination for your data streams.</Text>
            </Box>
          </Group>
        </Paper>

        <Paper p="xl" withBorder radius="md">
          <Suspense fallback={<FormSkeleton />}>
            <SinkForm />
          </Suspense>
        </Paper>
      </Stack>
    </Box>
  );
}


