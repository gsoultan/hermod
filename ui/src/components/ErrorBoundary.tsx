import { Component } from 'react';
import type { ErrorInfo, ReactNode } from 'react';
import { Alert, Container, Title, Text, Button, Stack, Paper, Group } from '@mantine/core';
import { IconAlertTriangle, IconRefresh } from '@tabler/icons-react';

interface Props {
  children: ReactNode;
}

interface State {
  hasError: boolean;
  error: Error | null;
}

export class ErrorBoundary extends Component<Props, State> {
  public state: State = {
    hasError: false,
    error: null,
  };

  public static getDerivedStateFromError(error: Error): State {
    return { hasError: true, error };
  }

  public componentDidCatch(error: Error, errorInfo: ErrorInfo) {
    console.error('Uncaught error:', error, errorInfo);
  }

  public render() {
    if (this.state.hasError) {
      return (
        <Container size="sm" py="xl">
          <Paper withBorder p="xl" radius="md" shadow="md">
            <Stack align="center" gap="lg">
              <IconAlertTriangle size="3rem" color="var(--mantine-color-red-6)" />
              <Stack gap="xs" align="center">
                <Title order={2}>Something went wrong</Title>
                <Text c="dimmed" ta="center">
                  An unexpected error occurred while rendering this part of the application.
                </Text>
              </Stack>
              
              <Alert color="red" variant="light" w="100%">
                <Text size="sm" style={{ wordBreak: 'break-all' }}>
                  {this.state.error?.message || 'Unknown error'}
                </Text>
              </Alert>

              <Group>
                <Button 
                  leftSection={<IconRefresh size="1rem" />}
                  onClick={() => window.location.reload()}
                >
                  Reload Page
                </Button>
                <Button 
                  variant="outline"
                  onClick={() => this.setState({ hasError: false, error: null })}
                >
                  Try Again
                </Button>
              </Group>
            </Stack>
          </Paper>
        </Container>
      );
    }

    return this.props.children;
  }
}
