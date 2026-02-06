import { Title, Text, Button, Container, Group, Stack, ThemeIcon } from '@mantine/core';import { useNavigate } from '@tanstack/react-router';import { IconArrowLeft, IconHome, IconSearch } from '@tabler/icons-react';
export function NotFoundPage() {
  const navigate = useNavigate();

  return (
    <Container size="md" py={80}>
      <Stack align="center" gap="xl">
        <ThemeIcon size={120} radius={120} color="indigo" variant="light">
          <IconSearch size="4rem" stroke={1.5} />
        </ThemeIcon>
        
        <Stack gap={5} align="center">
          <Title order={1} fw={900} size={34} ta="center">
            Page Not Found
          </Title>
          <Text c="dimmed" size="lg" ta="center" style={{ maxWidth: 500 }}>
            The page you are looking for might have been moved, deleted, or never existed. 
            Please check the URL or use the navigation below.
          </Text>
        </Stack>

        <Group>
          <Button 
            variant="light" 
            color="gray" 
            leftSection={<IconArrowLeft size="1.2rem" />}
            onClick={() => window.history.back()}
          >
            Go Back
          </Button>
          <Button 
            variant="filled"
            color="indigo" 
            leftSection={<IconHome size="1.2rem" />}
            onClick={() => navigate({ to: '/' })}
          >
            Go to Dashboard
          </Button>
        </Group>
      </Stack>
    </Container>
  );
}


