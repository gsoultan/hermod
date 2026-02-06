import { Title, Paper, Stack, Group, Box, Text } from '@mantine/core';
import { SourceForm } from '../components/SourceForm';
import { IconDatabaseImport } from '@tabler/icons-react';
export function AddSourcePage() {
  return (
    <Box p="md">
      <Stack gap="lg">
        <Paper p="md" withBorder radius="md" bg="gray.0">
          <Group gap="sm">
            <IconDatabaseImport size="2rem" color="var(--mantine-color-blue-filled)" />
            <Box>
              <Title order={2} fw={800}>Add New Source</Title>
              <Text size="sm" c="dimmed">Configure a new data source to capture changes.</Text>
            </Box>
          </Group>
        </Paper>

        <Paper p="xl" withBorder radius="md">
          <SourceForm />
        </Paper>
      </Stack>
    </Box>
  );
}


