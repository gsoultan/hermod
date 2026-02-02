import { useState } from 'react';
import { Stack, Text, Textarea, Button, Group, Code, ActionIcon, Tooltip, Paper, Badge, ThemeIcon, ScrollArea } from '@mantine/core';
import { IconSparkles, IconCopy, IconCheck, IconTrash, IconRobot } from '@tabler/icons-react';
import { apiFetch } from '../api';

export function AICopilot() {
  const [prompt, setPrompt] = useState('');
  const [result, setResult] = useState<any>(null);
  const [loading, setLoading] = useState(false);
  const [copied, setCopied] = useState(false);

  const handleGenerate = async () => {
    if (!prompt.trim()) return;
    setLoading(true);
    try {
      const res = await apiFetch('/api/ai/copilot', {
        method: 'POST',
        body: JSON.stringify({ prompt })
      });
      const data = await res.json();
      setResult(data);
    } catch (e) {
      console.error(e);
    } finally {
      setLoading(false);
    }
  };

  const copyToClipboard = () => {
    if (result?.code) {
      navigator.clipboard.writeText(result.code);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    }
  };

  return (
    <Stack gap="md">
      <Group gap="xs">
        <ThemeIcon variant="light" color="indigo" size="md">
          <IconRobot size="1.2rem" />
        </ThemeIcon>
        <Text fw={700} size="sm">AI Transformation Copilot</Text>
        <Badge variant="light" color="indigo" size="xs">BETA</Badge>
      </Group>

      <Text size="xs" c="dimmed">
        Describe the transformation logic you want to generate (e.g. "Convert currency from USD to EUR", "Filter out records where age is less than 18").
      </Text>

      <Textarea
        placeholder="Enter your transformation requirements..."
        minRows={3}
        value={prompt}
        onChange={(e) => setPrompt(e.currentTarget.value)}
        styles={{ input: { fontSize: '12px' } }}
      />

      <Button 
        leftSection={<IconSparkles size="1rem" />} 
        variant="gradient" 
        gradient={{ from: 'indigo', to: 'cyan' }}
        onClick={handleGenerate}
        loading={loading}
        size="xs"
      >
        Generate Logic
      </Button>

      {result && (
        <Paper withBorder p="xs" radius="md" bg="gray.0" pos="relative">
          <Group justify="space-between" mb="xs">
            <Badge size="xs" color="violet">{result.language || 'Lua'}</Badge>
            <Group gap={4}>
              <Tooltip label={copied ? 'Copied!' : 'Copy to clipboard'}>
                <ActionIcon variant="subtle" size="sm" onClick={copyToClipboard}>
                  {copied ? <IconCheck size="0.8rem" color="green" /> : <IconCopy size="0.8rem" />}
                </ActionIcon>
              </Tooltip>
              <ActionIcon variant="subtle" size="sm" color="red" onClick={() => setResult(null)}>
                <IconTrash size="0.8rem" />
              </ActionIcon>
            </Group>
          </Group>
          
          <ScrollArea h={200}>
            <Code block style={{ fontSize: '11px', whiteSpace: 'pre-wrap' }}>
              {result.code}
            </Code>
          </ScrollArea>
          
          {result.explanation && (
            <Text size="xs" mt="xs" c="dimmed" style={{ fontStyle: 'italic' }}>
              {result.explanation}
            </Text>
          )}
        </Paper>
      )}
    </Stack>
  );
}
