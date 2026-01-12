import { Title, Text, Button, Container, Group, Paper, Stack, ThemeIcon } from '@mantine/core';
import { IconAlertTriangle, IconRefresh, IconHome } from '@tabler/icons-react';
import { useNavigate, useRouter } from '@tanstack/react-router';

interface ErrorPageProps {
  error: Error;
  reset: () => void;
}

export function ErrorPage({ error, reset }: ErrorPageProps) {
  const navigate = useNavigate();
  const router = useRouter();

  return (
    <Container size="md" py={80}>
      <Paper p="xl" radius="md" withBorder style={{ backgroundColor: 'var(--mantine-color-red-0)' }}>
        <Stack align="center" gap="lg">
          <ThemeIcon size={80} radius={80} color="red" variant="light">
            <IconAlertTriangle size="3rem" stroke={1.5} />
          </ThemeIcon>
          
          <Stack gap={5} align="center">
            <Title order={1} fw={900} size={34} ta="center">
              Something went wrong
            </Title>
            <Text c="dimmed" size="lg" ta="center" style={{ maxWidth: 500 }}>
              An unexpected error occurred. The system is still running, but this specific page couldn't be loaded. 
              We've notified the system of this issue.
            </Text>
          </Stack>

          <Paper p="md" withBorder radius="sm" style={{ width: '100%', backgroundColor: 'white' }}>
            <Text fw={700} size="sm" mb={5} c="red">Error details:</Text>
            <Text ff="monospace" size="xs" style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>
              {error.message || 'Unknown error'}
            </Text>
          </Paper>

          <Group>
            <Button 
              variant="light" 
              color="gray" 
              leftSection={<IconHome size="1.2rem" />}
              onClick={() => navigate({ to: '/' })}
            >
              Go to Dashboard
            </Button>
            <Button 
              color="red" 
              leftSection={<IconRefresh size="1.2rem" />}
              onClick={() => {
                reset();
                router.invalidate();
              }}
            >
              Try again
            </Button>
          </Group>
        </Stack>
      </Paper>
    </Container>
  );
}
