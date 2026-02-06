import { useState, useEffect, useCallback } from 'react';
import { 
  Modal, Button, Stack, Text, Group, ThemeIcon, 
  Paper, Loader, Alert
} from '@mantine/core';import { notifications } from '@mantine/notifications';
import { apiFetch } from '../../../api';import { IconCheck, IconSettings, IconSparkles } from '@tabler/icons-react';
export function AIFixModal({ data, opened, onClose }: { 
  data: any, 
  opened: boolean, 
  onClose: () => void 
}) {
  const [loading, setLoading] = useState(false);
  const [suggestion, setSuggestion] = useState<any>(null);

  const handleAnalyze = useCallback(async () => {
    setLoading(true);
    setSuggestion(null);
    try {
      const res = await apiFetch('/api/ai/analyze-error', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          workflow_id: data.workflow_id,
          node_id: data.node_id,
          error: data.error
        })
      });
      if (res.ok) {
        setSuggestion(await res.json());
      }
    } catch (e) {}
    finally {
      setLoading(false);
    }
  }, [data]);

  useEffect(() => {
    if (opened && data) {
      handleAnalyze();
    }
  }, [opened, data, handleAnalyze]);

  const handleApply = () => {
    notifications.show({
      title: 'Fix Applied (Simulation)',
      message: 'The AI suggestion has been applied to the node configuration.',
      color: 'green',
      icon: <IconCheck size="1rem" />
    });
    onClose();
  };

  return (
    <Modal
      opened={opened}
      onClose={onClose}
      title={
        <Group gap="xs">
          <ThemeIcon variant="light" color="orange">
            <IconSparkles size="1.2rem" />
          </ThemeIcon>
          <Text fw={700}>AI Error Diagnostic</Text>
        </Group>
      }
    >
      <Stack gap="md">
        <Paper p="xs" withBorder bg="red.0">
          <Text size="xs" fw={700} c="red.9">DETECTED ERROR</Text>
          <Text size="sm" c="red.8" style={{ fontFamily: 'monospace' }}>{data?.error}</Text>
        </Paper>

        {loading ? (
          <Group justify="center" py="xl">
            <Loader size="sm" />
            <Text size="xs">AI is analyzing the failure pattern...</Text>
          </Group>
        ) : (
          <>
            {suggestion ? (
              <>
                <Alert color="indigo" icon={<IconSettings size="1rem" />} title="AI Suggestion">
                  <Text size="sm">{suggestion.explanation}</Text>
                </Alert>

                {suggestion.fix_action !== 'manual_review' && (
                  <Group justify="flex-end">
                    <Button variant="light" color="gray" onClick={onClose}>Ignore</Button>
                    <Button leftSection={<IconCheck size="1rem" />} color="indigo" onClick={handleApply}>
                      Apply Fix
                    </Button>
                  </Group>
                )}
              </>
            ) : (
              <Text size="sm" c="dimmed">Failed to generate AI suggestion. Please check logs manually.</Text>
            )}
          </>
        )}
      </Stack>
    </Modal>
  );
}


