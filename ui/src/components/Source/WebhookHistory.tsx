import { Card, Stack, Group, Text, ActionIcon, Divider, ScrollArea, Badge, Tooltip, Code } from '@mantine/core';
import { useMutation, useSuspenseQuery } from '@tanstack/react-query';
import { apiFetch } from '../../api';
import { formatDateTime } from '../../utils/dateUtils';
import { IconChevronRight, IconHistory, IconInfoCircle, IconPlayerPlay, IconRefresh } from '@tabler/icons-react';
const API_BASE = '/api';

interface WebhookHistoryProps {
  path: string;
  onReplaySuccess: () => void;
  onSelectSample: (body: string) => void;
}

export function WebhookHistory({ 
  path, 
  onReplaySuccess, 
  onSelectSample 
}: WebhookHistoryProps) {
  const { data: history, refetch, isLoading } = useSuspenseQuery({
    queryKey: ['webhook-history', path],
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/webhooks/requests?path=${encodeURIComponent(path)}&limit=10`);
      return res.json();
    }
  });

  const replayMutation = useMutation({
    mutationFn: async (id: string) => {
      const res = await apiFetch(`${API_BASE}/webhooks/requests/${id}/replay`, {
        method: 'POST'
      });
      if (!res.ok) throw new Error(await res.text());
      return res.json();
    },
    onSuccess: () => {
      onReplaySuccess();
    }
  });

  const requests = (history && Array.isArray((history as any).data)) ? (history as any).data : [];

  return (
    <Card withBorder shadow="sm" radius="md" p="md" h="100%" bg="var(--mantine-color-gray-0)">
      <Stack h="100%">
        <Group justify="space-between" px="xs">
          <Group gap="xs">
            <IconHistory size="1.2rem" color="var(--mantine-color-blue-6)" />
            <Text size="sm" fw={700} c="dimmed">3. WEBHOOK HISTORY</Text>
          </Group>
          <ActionIcon 
            variant="subtle" 
            size="sm" 
            onClick={() => refetch()} 
            loading={isLoading}
            aria-label="Refresh webhook history"
          >
            <IconRefresh size="1rem" />
          </ActionIcon>
        </Group>
        <Divider />
        
        {requests.length === 0 ? (
          <Stack align="center" py="xl" gap="sm" style={{ flex: 1, justifyContent: 'center' }}>
            <IconInfoCircle size="2rem" color="var(--mantine-color-gray-4)" />
            <Text size="xs" c="dimmed" ta="center">No recent webhook requests found for this path.</Text>
          </Stack>
        ) : (
          <ScrollArea flex={1}>
            <Stack gap="xs">
              {requests.map((req: any) => (
                <Card key={req.id} withBorder p="xs" radius="sm">
                  <Stack gap={4}>
                    <Group justify="space-between">
                      <Group gap={6}>
                        <Badge size="xs" color="blue" variant="filled">{req.method}</Badge>
                        <Text size="xs" fw={700}>{formatDateTime(req.timestamp)}</Text>
                      </Group>
                      <Group gap={4}>
                        <Tooltip label="Replay this request">
                          <ActionIcon 
                            size="sm" 
                            variant="light" 
                            color="green" 
                            onClick={() => replayMutation.mutate(req.id)}
                            loading={replayMutation.isPending && replayMutation.variables === req.id}
                            aria-label="Replay webhook request"
                          >
                            <IconPlayerPlay size="0.8rem" />
                          </ActionIcon>
                        </Tooltip>
                        <Tooltip label="Use as sample data">
                          <ActionIcon 
                            size="sm" 
                            variant="light" 
                            color="blue" 
                            onClick={() => onSelectSample(req.body)}
                            aria-label="Use as sample data"
                          >
                            <IconChevronRight size="0.8rem" />
                          </ActionIcon>
                        </Tooltip>
                      </Group>
                    </Group>
                    <Code block style={{ fontSize: '9px', maxHeight: '100px', overflow: 'hidden' }}>
                      {typeof req.body === 'string' ? req.body : JSON.stringify(req.body, null, 2)}
                    </Code>
                  </Stack>
                </Card>
              ))}
            </Stack>
          </ScrollArea>
        )}
      </Stack>
    </Card>
  );
}


