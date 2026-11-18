import { Alert, Button, Group, JsonInput, Modal, PasswordInput, Stack, Tabs, Text } from '@mantine/core';
import { IconBraces, IconInfoCircle, IconPlayerPlay } from '@tabler/icons-react';
import { useState } from 'react';
import { useShallow } from 'zustand/react/shallow';
import { apiFetch } from '@/api';
import { useWorkflowStore } from '../store/useWorkflowStore';

/**
 * The simulation's input: the message a Test run feeds the flow when no source
 * has a sample of its own. Opened by Configure & run in the editor and by the
 * same item on the read-only view.
 */
export function SimulationInputModal({ onRun, isTesting }: { onRun: () => void; isTesting: boolean }) {
  const { testModalOpened, testInput, setTestModalOpened, setTestInput } = useWorkflowStore(useShallow((state) => ({
    testModalOpened: state.testModalOpened,
    testInput: state.testInput,
    setTestModalOpened: state.setTestModalOpened,
    setTestInput: state.setTestInput,
  })));
  const [cryptoKey, setCryptoKey] = useState('');
  const [saveStatus, setSaveStatus] = useState<null | { ok: boolean; msg: string }>(null);

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
              onClick={() => onRun()}
              loading={isTesting}
            >
              Run Simulation
            </Button>
          </Group>
        </Stack>
      </Modal>
  );
}
