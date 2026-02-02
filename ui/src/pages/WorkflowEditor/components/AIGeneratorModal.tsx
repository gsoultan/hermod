import { useState } from 'react';
import { 
  Modal, TextInput, Button, Stack, Text, Group, ThemeIcon, 
  Paper, Code, Badge, Loader, ActionIcon, ScrollArea, Alert
} from '@mantine/core';
import { IconSparkles, IconPlayerPlay, IconRocket, IconAlertCircle } from '@tabler/icons-react';
import { notifications } from '@mantine/notifications';
import { apiFetch } from '../../../api';

export function AIGeneratorModal({ opened, onClose, onGenerated }: { 
  opened: boolean, 
  onClose: () => void,
  onGenerated: (workflow: any) => void 
}) {
  const [prompt, setPrompt] = useState('');
  const [loading, setLoading] = useState(false);
  const [suggestion, setSuggestion] = useState<any>(null);

  const handleGenerate = async () => {
    if (!prompt) return;
    setLoading(true);
    try {
      const res = await apiFetch('/api/ai/generate-workflow', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ prompt })
      });
      if (res.ok) {
        const data = await res.json();
        setSuggestion(data);
      } else {
        notifications.show({ title: 'AI Generation Failed', message: 'Failed to generate workflow scaffold.', color: 'red' });
      }
    } catch (e: any) {
      notifications.show({ title: 'Error', message: e.message, color: 'red' });
    } finally {
      setLoading(false);
    }
  };

  const handleApply = () => {
    onGenerated(suggestion);
    onClose();
    setSuggestion(null);
    setPrompt('');
  };

  return (
    <Modal
      opened={opened}
      onClose={onClose}
      title={
        <Group gap="xs">
          <ThemeIcon variant="light" color="indigo">
            <IconSparkles size="1.2rem" />
          </ThemeIcon>
          <Text fw={700}>AI Workflow Generator</Text>
        </Group>
      }
      size="lg"
    >
      <Stack gap="md">
        <Text size="sm" c="dimmed">
          Describe the data flow you want to create in plain English. 
          For example: "Sync Salesforce Leads to Snowflake and mask emails."
        </Text>

        <TextInput
          placeholder="What do you want to build?"
          value={prompt}
          onChange={(e) => setPrompt(e.currentTarget.value)}
          rightSection={
            <ActionIcon variant="light" color="indigo" onClick={handleGenerate} loading={loading}>
              <IconPlayerPlay size="1rem" />
            </ActionIcon>
          }
          onKeyDown={(e) => e.key === 'Enter' && handleGenerate()}
        />

        {loading && (
          <Paper p="xl" withBorder style={{ textAlign: 'center' }}>
            <Loader size="sm" mb="sm" />
            <Text size="xs">Consulting Hermod Intelligence...</Text>
          </Paper>
        )}

        {suggestion && (
          <Stack gap="sm">
            <Paper p="md" withBorder bg="gray.0">
              <Group justify="space-between" mb="xs">
                <Text size="xs" fw={700}>GENERATED SCAFFOLD</Text>
                <Badge color="green" variant="light">Ready</Badge>
              </Group>
              <ScrollArea h={200}>
                <Code block style={{ fontSize: '10px' }}>
                  {JSON.stringify(suggestion, null, 2)}
                </Code>
              </ScrollArea>
            </Paper>

            <Alert color="blue" icon={<IconAlertCircle size="1rem" />}>
              The AI has generated a scaffold. You will need to configure credentials and specific field mappings manually.
            </Alert>

            <Group justify="flex-end">
              <Button variant="subtle" color="gray" onClick={() => setSuggestion(null)}>Retry</Button>
              <Button leftSection={<IconRocket size="1rem" />} onClick={handleApply}>Apply to Canvas</Button>
            </Group>
          </Stack>
        )}
      </Stack>
    </Modal>
  );
}
