import { 
  Alert, Button, Group, JsonInput, Modal, Stack, Text, Tabs, PasswordInput, Table, ScrollArea, Badge, Loader
} from '@mantine/core';
import { useShallow } from 'zustand/react/shallow';
import { IconBraces, IconInfoCircle, IconPlayerPlay, IconSearch, IconEye, IconAlertCircle } from '@tabler/icons-react';
import { useWorkflowStore } from '../store/useWorkflowStore';
import { useState, useEffect, useCallback } from 'react';
import { apiFetch } from '../../../api';

interface ModalsProps {
  onRunSimulation: (input?: any) => void;
  isTesting: boolean;
}

export function Modals({ onRunSimulation, isTesting }: ModalsProps) {
  const { 
    testModalOpened, dlqInspectorOpened, dlqInspectorSink, testInput,
    setTestModalOpened, setDlqInspectorOpened, setTestInput
  } = useWorkflowStore(useShallow(state => ({
    testModalOpened: state.testModalOpened,
    dlqInspectorOpened: state.dlqInspectorOpened,
    dlqInspectorSink: state.dlqInspectorSink,
    testInput: state.testInput,
    setTestModalOpened: state.setTestModalOpened,
    setDlqInspectorOpened: state.setDlqInspectorOpened,
    setTestInput: state.setTestInput
  })));

  const [cryptoKey, setCryptoKey] = useState('');
  const [saveStatus, setSaveStatus] = useState<null | { ok: boolean; msg: string }>(null);

  // DLQ Inspector state
  const [failedMessages, setFailedMessages] = useState<any[]>([]);
  const [isLoadingMessages, setIsLoadingLoadingMessages] = useState(false);
  const [messagesError, setMessagesError] = useState<string | null>(null);

  const fetchFailedMessages = useCallback(async () => {
    if (!dlqInspectorSink) return;
    setIsLoadingLoadingMessages(true);
    setMessagesError(null);
    try {
      const res = await apiFetch('/api/sinks/browse', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          sink: dlqInspectorSink,
          table: dlqInspectorSink.config?.table || 'hermod_messages', // Guessing table name
          limit: 50
        })
      });
      if (res.ok) {
        const data = await res.json();
        setFailedMessages(data);
      } else {
        const err = await res.json();
        setMessagesError(err.error || 'Failed to fetch messages');
      }
    } catch (e: any) {
      setMessagesError(e.message);
    } finally {
      setIsLoadingLoadingMessages(false);
    }
  }, [dlqInspectorSink]);

  useEffect(() => {
    if (dlqInspectorOpened) {
      fetchFailedMessages();
    }
  }, [dlqInspectorOpened, fetchFailedMessages]);

  function generateKey(len = 32) {
    const bytes = new Uint8Array(len);
    if (typeof window !== 'undefined' && window.crypto?.getRandomValues) {
      window.crypto.getRandomValues(bytes);
    } else {
      for (let i = 0; i < len; i++) bytes[i] = Math.floor(Math.random() * 256);
    }
    const b64 = btoa(String.fromCharCode(...Array.from(bytes)))
      .replace(/\+/g, '-')
      .replace(/\//g, '_')
      .replace(/=+$/g, '');
    return b64.slice(0, Math.max(16, Math.min(64, len)));
  }

  async function saveCryptoKey() {
    setSaveStatus(null);
    try {
      const res = await apiFetch('/api/config/crypto', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ crypto_master_key: cryptoKey })
      });
      if (!res.ok) {
        const txt = await res.text();
        setSaveStatus({ ok: false, msg: txt || 'Failed to save key' });
      } else {
        setSaveStatus({ ok: true, msg: 'Crypto master key saved.' });
      }
    } catch (e: any) {
      setSaveStatus({ ok: false, msg: e?.message || 'Failed to save key' });
    }
  }

  return (
    <>
    <Modal
      opened={testModalOpened}
      onClose={() => setTestModalOpened(false)}
      title={
        <Group gap="xs">
          <IconBraces size="1.2rem" color="var(--mantine-color-blue-6)" />
          <Text fw={700}>Run Flow Simulation</Text>
        </Group>
      }
      size="lg"
    >
      <Stack gap="md">
        <Alert icon={<IconInfoCircle size="1rem" />} color="blue">
          Simulation allows you to test your workflow logic without actually sending data to sinks.
          Provide a sample JSON payload to see how it moves through the nodes.
        </Alert>

        <Tabs defaultValue="input">
          <Tabs.List>
            <Tabs.Tab value="input">Input</Tabs.Tab>
            <Tabs.Tab value="security">Security</Tabs.Tab>
          </Tabs.List>

          <Tabs.Panel value="input" pt="md">
            <JsonInput
              label="Input Message (JSON)"
              placeholder='{"id": 1, "status": "active"}'
              validationError="Invalid JSON"
              formatOnBlur
              autosize
              minRows={8}
              maxRows={15}
              value={testInput}
              onChange={setTestInput}
            />
          </Tabs.Panel>

          <Tabs.Panel value="security" pt="md">
            <Stack gap="sm">
              <Alert color="gray">
                crypto_master_key secures encryption of sensitive connector settings. Keep it safe and back it up.
              </Alert>
              <PasswordInput
                label="Crypto Master Key"
                placeholder="Not set"
                value={cryptoKey}
                onChange={(e) => setCryptoKey(e.currentTarget.value)}
              />
              <Group justify="space-between">
                <Button variant="light" onClick={() => setCryptoKey(generateKey(32))}>Generate</Button>
                <Group>
                  <Button
                    variant="default"
                    onClick={() => {
                      if (navigator?.clipboard && cryptoKey) navigator.clipboard.writeText(cryptoKey)
                    }}
                    disabled={!cryptoKey}
                  >
                    Copy
                  </Button>
                  <Button onClick={saveCryptoKey}>Save Key (Admin)</Button>
                </Group>
              </Group>
              {saveStatus && (
                <Alert color={saveStatus.ok ? 'green' : 'red'}>{saveStatus.msg}</Alert>
              )}
            </Stack>
          </Tabs.Panel>
        </Tabs>

        <Group justify="flex-end" mt="md">
          <Button variant="light" onClick={() => setTestModalOpened(false)}>Cancel</Button>
          <Button 
            leftSection={<IconPlayerPlay size="1rem" />} 
            onClick={() => onRunSimulation()}
            loading={isTesting}
          >
            Run Simulation
          </Button>
        </Group>
      </Stack>
    </Modal>

    <Modal
      opened={dlqInspectorOpened}
      onClose={() => setDlqInspectorOpened(false)}
      title={
        <Group gap="xs">
          <IconSearch size="1.2rem" color="var(--mantine-color-orange-6)" />
          <Text fw={700}>Dead Letter Sink Inspector: {dlqInspectorSink?.name}</Text>
        </Group>
      }
      size="xl"
    >
      <Stack gap="md">
        <Alert icon={<IconInfoCircle size="1rem" />} color="orange">
          Below are messages that failed all retry attempts and were redirected to this DLQ sink.
          Click a message to view failure details and original payload.
        </Alert>

        {messagesError && (
          <Alert icon={<IconAlertCircle size="1rem" />} color="red" title="Error fetching messages">
            {messagesError}
          </Alert>
        )}

        <ScrollArea h={400}>
          <Table verticalSpacing="xs">
            <Table.Thead>
              <Table.Tr>
                <Table.Th>Time</Table.Th>
                <Table.Th>Original Sink</Table.Th>
                <Table.Th>Error</Table.Th>
                <Table.Th>Actions</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {isLoadingMessages ? (
                <Table.Tr><Table.Td colSpan={4}><Group justify="center" py="xl"><Loader size="sm" /><Text size="sm">Loading messages...</Text></Group></Table.Td></Table.Tr>
              ) : failedMessages.length === 0 ? (
                <Table.Tr><Table.Td colSpan={4}><Text ta="center" py="xl" c="dimmed">No failed messages found in this sink.</Text></Table.Td></Table.Tr>
              ) : failedMessages.map((msg, idx) => (
                <Table.Tr key={msg.id || idx}>
                  <Table.Td>
                    <Text size="xs">{msg.metadata?._hermod_failed_at ? new Date(msg.metadata._hermod_failed_at).toLocaleString() : 'Unknown'}</Text>
                  </Table.Td>
                  <Table.Td>
                    <Badge size="xs" variant="outline">{msg.metadata?._hermod_failed_sink || 'Unknown'}</Badge>
                  </Table.Td>
                  <Table.Td>
                    <Text size="xs" truncate maw={300} c="red">{msg.metadata?._hermod_last_error || 'No error details'}</Text>
                  </Table.Td>
                  <Table.Td>
                    <Button variant="subtle" size="xs" leftSection={<IconEye size="0.8rem" />} onClick={() => {
                      setTestInput(msg.after || msg.payload || '{}');
                      setDlqInspectorOpened(false);
                      setTestModalOpened(true);
                    }}>
                      Inspect
                    </Button>
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </ScrollArea>

        <Group justify="flex-end">
          <Button variant="outline" color="orange" onClick={fetchFailedMessages} loading={isLoadingMessages}>Refresh</Button>
          <Button onClick={() => setDlqInspectorOpened(false)}>Close</Button>
        </Group>
      </Stack>
    </Modal>
    </>
  );
}
