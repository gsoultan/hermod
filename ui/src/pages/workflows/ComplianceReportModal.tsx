import { Alert, Button, Group, Loader, Modal, Paper, ScrollArea, Stack, Text } from '@mantine/core';
import { useQuery } from '@tanstack/react-query';
import { IconAlertCircle, IconShieldLock } from '@tabler/icons-react';
import { apiFetch } from '@/api';

interface ComplianceReportModalProps {
  workflowId: string | undefined;
  opened: boolean;
  onClose: () => void;
}

/** The flow's compliance report, read on open, with text and PDF downloads. Offered by the view and the editor alike. */
export function ComplianceReportModal({ workflowId, opened, onClose }: ComplianceReportModalProps) {
  const { data: reportText, isLoading, error } = useQuery({
    queryKey: ['workflow-report', workflowId],
    enabled: opened && Boolean(workflowId),
    staleTime: 0,
    queryFn: async () => {
      const res = await apiFetch(`/api/workflows/${workflowId}/report`);
      if (!res.ok) {
        const err = await res.json().catch(() => ({}));
        throw new Error(err.error || 'Failed to load report');
      }
      return res.text();
    },
  });

  const download = async (format: 'pdf' | 'text') => {
    try {
      const res = await apiFetch(`/api/workflows/${workflowId}/report?format=${format}`);
      if (!res.ok) return;
      const blob = await res.blob();
      const url = window.URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = `compliance_report_${workflowId}.${format === 'pdf' ? 'pdf' : 'txt'}`;
      document.body.appendChild(a);
      a.click();
      a.remove();
      window.URL.revokeObjectURL(url);
    } catch (e) {
      console.error('Failed to download report', e);
    }
  };

  return (
    <Modal
      opened={opened}
      onClose={onClose}
      title={(
        <Group gap="xs">
          <IconShieldLock size="1.2rem" color="var(--mantine-color-orange-6)" />
          <Text fw={700}>Compliance report</Text>
        </Group>
      )}
      size="lg"
    >
      <Stack gap="md">
        <Alert color="orange" icon={<IconShieldLock size="1.2rem" />} title="Security certification">
          This report summarizes the security, privacy, and integrity status of the flow.
        </Alert>
        {isLoading ? (
          <Group justify="center" py="xl"><Loader size="md" /></Group>
        ) : error ? (
          <Alert color="red" icon={<IconAlertCircle size="1rem" />}>{(error as Error).message}</Alert>
        ) : (
          <>
            <Paper withBorder p="md" bg="var(--mantine-color-body)">
              <ScrollArea.Autosize mah={400}>
                <Text style={{ whiteSpace: 'pre-wrap', fontFamily: 'monospace', fontSize: '13px' }}>{reportText}</Text>
              </ScrollArea.Autosize>
            </Paper>
            <Group justify="flex-end">
              <Button variant="light" color="blue" onClick={() => download('text')}>Download text</Button>
              <Button color="orange" onClick={() => download('pdf')}>Download PDF</Button>
            </Group>
          </>
        )}
      </Stack>
    </Modal>
  );
}
