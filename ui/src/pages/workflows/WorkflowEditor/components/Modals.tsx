import { 
  Alert, Button, Group, JsonInput, Modal, Stack, Text, Tabs, PasswordInput, ScrollArea, Badge, Loader, TextInput, ThemeIcon, Code, Box, Tooltip
} from '@mantine/core';
import { useShallow } from 'zustand/react/shallow';
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore';
import { useState, useEffect, useCallback } from 'react';
import { useParams } from '@tanstack/react-router';
import { apiFetch } from '../../../../api';
import { ComplianceReportModal } from '../../ComplianceReportModal';
import { SampleInspector } from './SampleInspector';
import { formatDateTime } from '../../../../utils/dateUtils';
import { IconAlertCircle, IconBraces, IconCircleCheck, IconCircleX, IconClock, IconInfoCircle, IconPlayerPlay, IconSearch, IconTimeline } from '@tabler/icons-react';
interface ModalsProps {
  onRunSimulation: (input?: any) => void;
  isTesting: boolean;
}

export function Modals({ onRunSimulation, isTesting }: ModalsProps) {
  const { id: workflowID } = useParams({ strict: false }) as any;
  const { 
    testModalOpened, testInput,
    traceInspectorOpened, traceMessageID,
    complianceReportOpened,
    setTestModalOpened, setTestInput,
    setTraceInspectorOpened,
    setComplianceReportOpened,
    nodes
  } = useWorkflowStore(useShallow(state => ({
    testModalOpened: state.testModalOpened,
    testInput: state.testInput,
    traceInspectorOpened: state.traceInspectorOpened,
    traceMessageID: state.traceMessageID,
    complianceReportOpened: state.complianceReportOpened,
    setTestModalOpened: state.setTestModalOpened,
    setTestInput: state.setTestInput,
    setTraceInspectorOpened: state.setTraceInspectorOpened,
    setComplianceReportOpened: state.setComplianceReportOpened,
    nodes: state.nodes
  })));

  const [cryptoKey, setCryptoKey] = useState('');
  const [saveStatus, setSaveStatus] = useState<null | { ok: boolean; msg: string }>(null);


  // Trace Inspector state
  const [traceData, setTraceData] = useState<any | null>(null);
  const [isLoadingTrace, setIsLoadingTrace] = useState(false);
  const [traceError, setTraceError] = useState<string | null>(null);
  const [searchID, setSearchID] = useState(traceMessageID);

  useEffect(() => {
    setSearchID(traceMessageID);
  }, [traceMessageID]);

  const fetchTrace = useCallback(async (msgID: string) => {
    if (!msgID || !workflowID) return;
    
    // Extract real ID if it's in format "message_id: ID, payload_len: ..."
    let targetID = msgID;
    if (targetID.includes('message_id: ')) {
      const match = targetID.match(/message_id: ([^,]+)/);
      if (match) targetID = match[1].trim();
    }

    setIsLoadingTrace(true);
    setTraceError(null);
    try {
      // Use query parameter for msgID to avoid issues with slashes in IDs (e.g. Postgres LSNs)
      const res = await apiFetch(`/api/workflows/${workflowID}/traces/?message_id=${encodeURIComponent(targetID || '')}`);
      if (res.ok) {
        const data = await res.json();
        setTraceData(data);
      } else {
        const err = await res.json();
        setTraceError(err.error || 'Trace not found');
        setTraceData(null);
      }
    } catch (e: any) {
      setTraceError(e.message);
      setTraceData(null);
    } finally {
      setIsLoadingTrace(false);
    }
  }, [workflowID]);

  useEffect(() => {
    if (traceInspectorOpened && traceMessageID) {
      fetchTrace(traceMessageID);
    }
  }, [traceInspectorOpened, traceMessageID, fetchTrace]);

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
      opened={traceInspectorOpened}
      onClose={() => setTraceInspectorOpened(false)}
      title={
        <Group gap="xs">
          <IconTimeline size="1.2rem" color="var(--mantine-color-blue-6)" />
          <Text fw={700}>Message Trace Inspector</Text>
        </Group>
      }
      size="xl"
    >
      <Stack gap="md">
        <TextInput 
          label="Message ID"
          placeholder="Enter Message ID to trace..."
          value={searchID}
          onChange={(e) => setSearchID(e.currentTarget.value)}
          rightSection={
            <Button size="compact-xs" variant="subtle" onClick={() => fetchTrace(searchID)}>
              <IconSearch size="1rem" />
            </Button>
          }
        />

        {traceError && (
          <Alert icon={<IconAlertCircle size="1rem" />} color="red">
            {traceError}
          </Alert>
        )}

        {isLoadingTrace ? (
          <Group justify="center" py="xl"><Loader size="md" /><Text>Loading trace data...</Text></Group>
        ) : traceData ? (
          <Stack gap="sm">
            <Group justify="space-between">
               <Text size="sm">Trace for: <Code>{traceData.message_id}</Code></Text>
               <Badge leftSection={<IconClock size="0.8rem" />} variant="light">
                  {formatDateTime(traceData.created_at)}
               </Badge>
            </Group>

            <ScrollArea h={400} offsetScrollbars>
               <Stack gap="xs" p="xs">
                  {traceData.steps?.map((step: any, idx: number) => (
                    <Box key={idx} style={{ 
                      borderLeft: '2px solid var(--mantine-color-blue-2)', 
                      paddingLeft: '1rem',
                      position: 'relative'
                    }}>
                      <ThemeIcon 
                        variant="filled" 
                        size="sm" 
                        radius="xl" 
                        color={step.error ? "red" : "blue"}
                        style={{ position: 'absolute', left: '-11px', top: '0' }}
                      >
                        {step.error ? <IconCircleX size="0.8rem" /> : <IconCircleCheck size="0.8rem" />}
                      </ThemeIcon>
                      
                      <Group justify="space-between" align="center">
                        {(() => {
                          const node = Array.isArray(nodes) ? nodes.find((n: any) => n.id === step.node_id) : null;
                          const type = (node?.type as string | undefined) || undefined;
                          const label = (node?.data?.label as string | undefined) || (node?.data?.name as string | undefined);
                          const subType = (node?.data?.transType as string | undefined) || (node?.data?.type as string | undefined);
                          const cap = (s?: string) => s ? s.charAt(0).toUpperCase() + s.slice(1) : undefined;
                          const shortId = (id: string) => id.length > 18 ? `${id.slice(0, 10)}…${id.slice(-4)}` : id;
                          return (
                            <Group gap="xs" wrap="nowrap">
                              {type && <Badge size="xs" variant="light">{cap(type)}</Badge>}
                              <Text fw={600} size="sm">{label || 'Unknown node'}</Text>
                              {subType && (
                                <Badge size="xs" variant="outline" color="gray">{subType}</Badge>
                              )}
                              <Tooltip label={step.node_id} withArrow>
                                <Code fz="xs" c="dimmed">{shortId(step.node_id)}</Code>
                              </Tooltip>
                            </Group>
                          );
                        })()}
                        <Text size="xs" c="dimmed">{step.duration_ms || Math.round(step.duration / 1000000)}ms</Text>
                      </Group>
                      
                      {step.error && (
                        <Text size="xs" c="red" mt={4}>Error: {step.error}</Text>
                      )}
                      
                      {step.data && (
                        <Box mt="xs">
                          <Text size="xs" fw={500} mb={4}>Output Data:</Text>
                          <ScrollArea h={100} offsetScrollbars type="auto">
                            <Code block>{JSON.stringify(step.data, null, 2)}</Code>
                          </ScrollArea>
                        </Box>
                      )}
                    </Box>
                  ))}
               </Stack>
            </ScrollArea>
          </Stack>
        ) : (
          <Alert color="gray" icon={<IconInfoCircle size="1rem" />}>
            Enter a Message ID above to visualize its journey through the workflow.
          </Alert>
        )}

        <Group justify="flex-end">
          <Button onClick={() => setTraceInspectorOpened(false)}>Close</Button>
        </Group>
      </Stack>
    </Modal>
      <SampleInspector />

      <ComplianceReportModal workflowId={workflowID} opened={complianceReportOpened} onClose={() => setComplianceReportOpened(false)} />
    </>
  );
}


