import { 
  Modal, Stack, Group, Text, Button, Table, Badge, ScrollArea, ThemeIcon, Alert, Loader
} from '@mantine/core';
import { 
  IconHistory, IconRotateDot, IconInfoCircle, IconAlertCircle
} from '@tabler/icons-react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { apiFetch } from '../api';

interface WorkflowHistoryModalProps {
  workflowId: string;
  opened: boolean;
  onClose: () => void;
  onRollbackSuccess?: () => void;
}

export function WorkflowHistoryModal({ workflowId, opened, onClose, onRollbackSuccess }: WorkflowHistoryModalProps) {
  const queryClient = useQueryClient();

  const { data: versions, isLoading, error } = useQuery({
    queryKey: ['versions', workflowId],
    queryFn: async () => {
      const res = await apiFetch(`/api/workflows/${workflowId}/versions`);
      if (!res.ok) throw new Error('Failed to fetch history');
      return res.json();
    },
    enabled: opened && !!workflowId
  });

  const rollbackMutation = useMutation({
    mutationFn: async (version: number) => {
      if (!confirm(`Rollback workflow to version ${version}?`)) return;
      const res = await apiFetch(`/api/workflows/${workflowId}/rollback/${version}`, {
        method: 'POST'
      });
      if (!res.ok) throw new Error(await res.text());
      return res.json();
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['workflow', workflowId] });
      queryClient.invalidateQueries({ queryKey: ['versions', workflowId] });
      if (onRollbackSuccess) onRollbackSuccess();
      onClose();
    }
  });

  return (
    <Modal 
      opened={opened} 
      onClose={onClose} 
      title={
        <Group gap="xs">
          <ThemeIcon variant="light" color="indigo">
            <IconHistory size="1.2rem" />
          </ThemeIcon>
          <Text fw={700}>Workflow Version History</Text>
        </Group>
      }
      size="xl"
    >
      <Stack gap="md">
        <Text size="sm" c="dimmed">
          Versions are automatically created every time you save changes to the workflow.
          You can restore any previous version to revert changes.
        </Text>

        {isLoading ? (
          <Group justify="center" p="xl"><Loader size="md" /></Group>
        ) : error ? (
          <Alert color="red" icon={<IconAlertCircle size="1rem" />}>
            {String(error)}
          </Alert>
        ) : (versions as any)?.length === 0 ? (
          <Alert color="gray" icon={<IconInfoCircle size="1rem" />}>
            No history found for this workflow.
          </Alert>
        ) : (
          <ScrollArea h={400}>
            <Table verticalSpacing="sm" highlightOnHover>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th style={{ width: 80 }}>Version</Table.Th>
                  <Table.Th style={{ width: 180 }}>Timestamp</Table.Th>
                  <Table.Th style={{ width: 120 }}>Created By</Table.Th>
                  <Table.Th>Message</Table.Th>
                  <Table.Th style={{ width: 120 }}>Actions</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {(Array.isArray(versions) ? versions : []).map((v: any) => (
                  <Table.Tr key={v.id}>
                    <Table.Td><Badge size="md">v{v.version}</Badge></Table.Td>
                    <Table.Td><Text size="sm">{new Date(v.created_at).toLocaleString()}</Text></Table.Td>
                    <Table.Td><Text size="sm">{v.created_by}</Text></Table.Td>
                    <Table.Td><Text size="sm">{v.message}</Text></Table.Td>
                    <Table.Td>
                      <Button 
                        variant="light" 
                        size="xs" 
                        color="orange" 
                        leftSection={<IconRotateDot size="0.8rem" />}
                        onClick={() => rollbackMutation.mutate(v.version)}
                        loading={rollbackMutation.isPending}
                      >
                        Restore
                      </Button>
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </ScrollArea>
        )}

        <Group justify="flex-end" mt="md">
          <Button variant="default" onClick={onClose}>Close</Button>
        </Group>
      </Stack>
    </Modal>
  );
}
