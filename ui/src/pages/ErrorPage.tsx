import { Text, Button, Container, Group, Paper, Stack, ThemeIcon, Accordion, Code, Box } from '@mantine/core';
import { useNavigate, useRouter } from '@tanstack/react-router';
import { IconAlertTriangle, IconHome, IconRefresh, IconBug, IconChevronRight } from '@tabler/icons-react';

interface ErrorPageProps {
  error: Error;
  reset: () => void;
}

export function ErrorPage({ error, reset }: ErrorPageProps) {
  const navigate = useNavigate();
  const router = useRouter();

  return (
    <Container size="sm" py={100}>
      <Stack align="center" gap="xl">
        <Box style={{ position: 'relative' }}>
          <ThemeIcon size={100} radius={100} color="red" variant="light" style={{ marginBottom: 20 }}>
            <IconAlertTriangle size="3.5rem" stroke={1.5} />
          </ThemeIcon>
          <ThemeIcon 
            size={32} 
            radius="xl" 
            color="red" 
            variant="filled" 
            style={{ 
              position: 'absolute', 
              bottom: 25, 
              right: -5,
              border: '4px solid white'
            }}
          >
            <IconBug size="1.2rem" />
          </ThemeIcon>
        </Box>
        
        <Stack gap="xs" align="center">
          <Text fw={900} size="38px" ta="center" variant="gradient" gradient={{ from: 'red', to: 'orange', deg: 45 }} style={{ lineHeight: 1.2 }}>
            Oops! System Interruption
          </Text>
          <Text c="dimmed" size="lg" ta="center" style={{ maxWidth: 520, lineHeight: 1.6 }}>
            Something unexpected happened. We've encountered a technical issue that prevents this page from loading correctly.
          </Text>
        </Stack>

        <Paper withBorder radius="lg" p={0} style={{ width: '100%', overflow: 'hidden', backgroundColor: 'var(--mantine-color-gray-0)' }}>
          <Accordion variant="separated" styles={{ 
            item: { border: 'none', backgroundColor: 'transparent' },
            control: { padding: '16px 20px' },
            panel: { padding: '0 20px 20px 20px' }
          }}>
            <Accordion.Item value="details">
              <Accordion.Control icon={<IconBug size="1.2rem" color="var(--mantine-color-red-6)" />}>
                <Text fw={600} size="sm">Technical Details</Text>
              </Accordion.Control>
              <Accordion.Panel>
                <Code block color="red.0" c="red.9" p="md" style={{ border: '1px solid var(--mantine-color-red-2)', borderRadius: 'var(--mantine-radius-md)' }}>
                  {error.name}: {error.message || 'Unknown error'}
                  {error.stack && (
                    <Box mt="md" pt="md" style={{ borderTop: '1px solid var(--mantine-color-red-1)', opacity: 0.7, fontSize: '11px' }}>
                      {error.stack.split('\n').slice(0, 5).join('\n')}
                    </Box>
                  )}
                </Code>
              </Accordion.Panel>
            </Accordion.Item>
          </Accordion>
        </Paper>

        <Group gap="md">
          <Button 
            variant="default" 
            size="lg"
            radius="md"
            leftSection={<IconHome size="1.2rem" />}
            onClick={() => navigate({ to: '/' })}
            styles={{ root: { borderWidth: 2 } }}
          >
            Back to Dashboard
          </Button>
          <Button 
            color="indigo" 
            size="lg"
            radius="md"
            leftSection={<IconRefresh size="1.2rem" />}
            onClick={() => {
              reset();
              router.invalidate();
            }}
            rightSection={<IconChevronRight size="1.2rem" />}
          >
            Retry Operation
          </Button>
        </Group>

        <Text size="xs" c="dimmed" ta="center">
          If this persists, please contact your system administrator.
        </Text>
      </Stack>
    </Container>
  );
}


