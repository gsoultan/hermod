import { Text, Button, Container, Group, Stack, ThemeIcon, Box } from '@mantine/core';
import { useNavigate } from '@tanstack/react-router';
import { IconArrowLeft, IconHome, IconSearch, IconCompass } from '@tabler/icons-react';

export function NotFoundPage() {
  const navigate = useNavigate();

  return (
    <Container size="sm" py={100}>
      <Stack align="center" gap="xl">
        <Box style={{ position: 'relative' }}>
          <ThemeIcon size={100} radius={100} color="indigo" variant="light" style={{ marginBottom: 20 }}>
            <IconSearch size="3.5rem" stroke={1.5} />
          </ThemeIcon>
          <ThemeIcon 
            size={32} 
            radius="xl" 
            color="indigo" 
            variant="filled" 
            style={{ 
              position: 'absolute', 
              bottom: 25, 
              right: -5,
              border: '4px solid white'
            }}
          >
            <IconCompass size="1.2rem" />
          </ThemeIcon>
        </Box>
        
        <Stack gap="xs" align="center">
          <Text fw={900} size="38px" ta="center" variant="gradient" gradient={{ from: 'indigo', to: 'cyan', deg: 45 }} style={{ lineHeight: 1.2 }}>
            Lost in Space?
          </Text>
          <Text c="dimmed" size="lg" ta="center" style={{ maxWidth: 520, lineHeight: 1.6 }}>
            The page you are looking for has vanished into the digital void. 
            It might have been moved, deleted, or never existed in the first place.
          </Text>
        </Stack>

        <Group gap="md">
          <Button 
            variant="default" 
            size="lg"
            radius="md"
            leftSection={<IconArrowLeft size="1.2rem" />}
            onClick={() => window.history.back()}
            styles={{ root: { borderWidth: 2 } }}
          >
            Go Back
          </Button>
          <Button 
            color="indigo" 
            size="lg"
            radius="md"
            leftSection={<IconHome size="1.2rem" />}
            onClick={() => navigate({ to: '/' })}
          >
            Return to Dashboard
          </Button>
        </Group>

        <Text size="xs" c="dimmed" ta="center">
          Error Code: 404 - Not Found
        </Text>
      </Stack>
    </Container>
  );
}


