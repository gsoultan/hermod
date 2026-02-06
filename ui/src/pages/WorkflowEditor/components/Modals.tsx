import { 
  Alert, Button, Group, JsonInput, Modal, Stack, Text, Tabs, PasswordInput, Table, ScrollArea, Badge, Loader, TextInput, ThemeIcon, Code, Box, Paper
} from '@mantine/core';
import { useShallow } from 'zustand/react/shallow';import { useWorkflowStore } from '../store/useWorkflowStore';
import { useState, useEffect, useCallback } from 'react';
import { useParams } from '@tanstack/react-router';
import { apiFetch } from '../../../api';
import { formatDateTime } from '../../../utils/dateUtils';import { IconAlertCircle, IconBraces, IconCircleCheck, IconCircleX, IconClock, IconEye, IconInfoCircle, IconPlayerPlay, IconSearch, IconShieldLock, IconTimeline } from '@tabler/icons-react';
interface ModalsProps {
  onRunSimulation: (input?: any) => void;
  isTesting: boolean;
}

export function Modals({ onRunSimulation, isTesting }: ModalsProps) {
  const { id: workflowID } = useParams({ strict: false }) as any;
  const { 
    testModalOpened, dlqInspectorOpened, dlqInspectorSink, testInput,
    traceInspectorOpened, traceMessageID,
    sampleInspectorOpened, sampleNodeId, nodeSamples,
    complianceReportOpened,
    setTestModalOpened, setDlqInspectorOpened, setTestInput,
    setTraceInspectorOpened, setSampleInspectorOpened,
    setComplianceReportOpened
  } = useWorkflowStore(useShallow(state => ({
    testModalOpened: state.testModalOpened,
    dlqInspectorOpened: state.dlqInspectorOpened,
    dlqInspectorSink: state.dlqInspectorSink,
    testInput: state.testInput,
    traceInspectorOpened: state.traceInspectorOpened,
    traceMessageID: state.traceMessageID,
    sampleInspectorOpened: state.sampleInspectorOpened,
    sampleNodeId: state.sampleNodeId,
    nodeSamples: state.nodeSamples,
    complianceReportOpened: state.complianceReportOpened,
    setTestModalOpened: state.setTestModalOpened,
    setDlqInspectorOpened: state.setDlqInspectorOpened,
    setTestInput: state.setTestInput,
    setTraceInspectorOpened: state.setTraceInspectorOpened,
    setSampleInspectorOpened: state.setSampleInspectorOpened,
    setComplianceReportOpened: state.setComplianceReportOpened
  })));

  const [cryptoKey, setCryptoKey] = useState('');
  const [saveStatus, setSaveStatus] = useState<null | { ok: boolean; msg: string }>(null);

  // DLQ Inspector state
  const [failedMessages, setFailedMessages] = useState<any[]>([]);
  const [isLoadingMessages, setIsLoadingLoadingMessages] = useState(false);
  const [messagesError, setMessagesError] = useState<string | null>(null);

  // Trace Inspector state
  const [traceData, setTraceData] = useState<any | null>(null);
  const [isLoadingTrace, setIsLoadingTrace] = useState(false);
  const [traceError, setTraceError] = useState<string | null>(null);
  const [searchID, setSearchID] = useState(traceMessageID);

  // Compliance Report state
  const [reportText, setReportText] = useState('');
  const [isLoadingReport, setIsLoadingReport] = useState(false);
  const [reportError, setReportError] = useState<string | null>(null);

  useEffect(() => {
    if (complianceReportOpened && workflowID) {
      setIsLoadingReport(true);
      setReportError(null);
      apiFetch(`/api/workflows/${workflowID}/report`)
        .then(async res => {
          if (res.ok) {
            const text = await res.text();
            setReportText(text);
          } else {
            const err = await res.json();
            setReportError(err.error || 'Failed to load report');
          }
        })
        .catch(e => setReportError(e.message))
        .finally(() => setIsLoadingReport(false));
    }
  }, [complianceReportOpened, workflowID]);

  const downloadReport = async (format: 'pdf' | 'text') => {
    try {
      const res = await apiFetch(`/api/workflows/${workflowID}/report?format=${format}`);
      if (res.ok) {
        const blob = await res.blob();
        const url = window.URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = url;
        a.download = `compliance_report_${workflowID}.${format === 'pdf' ? 'pdf' : 'txt'}`;
        document.body.appendChild(a);
        a.click();
        window.URL.revokeObjectURL(url);
      }
    } catch (e) {
      console.error('Failed to download report', e);
    }
  };

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
              ) : (Array.isArray(failedMessages) ? failedMessages : []).length === 0 ? (
                <Table.Tr><Table.Td colSpan={4}><Text ta="center" py="xl" c="dimmed">No failed messages found in this sink.</Text></Table.Td></Table.Tr>
              ) : (Array.isArray(failedMessages) ? failedMessages : []).map((msg, idx) => (
                <Table.Tr key={msg.id || idx}>
                  <Table.Td>
                    <Text size="xs">{formatDateTime(msg.metadata?._hermod_failed_at)}</Text>
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
                      
                      <Group justify="space-between">
                        <Text fw={600} size="sm">Node: {step.node_id}</Text>
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
      {/* Sample Inspector Modal */}
      <Modal
        opened={sampleInspectorOpened}
        onClose={() => setSampleInspectorOpened(false)}
        title={
          <Group gap="xs">
            <IconEye size="1.2rem" color="var(--mantine-color-blue-6)" />
            <Text fw={700}>Node Sample Inspector</Text>
          </Group>
        }
        size="lg"
      >
        <Stack>
          <Text size="sm" c="dimmed">
            This is the latest message successfully processed by node <b>{sampleNodeId}</b>.
          </Text>
          {sampleNodeId && nodeSamples[sampleNodeId] ? (
            <JsonInput
              label="Message Data"
              value={JSON.stringify(nodeSamples[sampleNodeId], null, 2)}
              readOnly
              autosize
              minRows={10}
              styles={{ input: { fontFamily: 'monospace', fontSize: '13px' } }}
            />
          ) : (
            <Alert icon={<IconInfoCircle size="1rem" />} color="blue">
              No sample available for this node yet. Start the workflow to capture live data.
            </Alert>
          )}
          <Group justify="flex-end">
            <Button onClick={() => setSampleInspectorOpened(false)}>Close</Button>
          </Group>
        </Stack>
      </Modal>

      {/* Compliance Report Modal */}
      <Modal
        opened={complianceReportOpened}
        onClose={() => setComplianceReportOpened(false)}
        title={
          <Group gap="xs">
            <IconShieldLock size="1.2rem" color="var(--mantine-color-orange-6)" />
            <Text fw={700}>Workflow Compliance Report</Text>
          </Group>
        }
        size="lg"
      >
        <Stack gap="md">
          <Alert color="orange" icon={<IconShieldLock size="1.2rem" />} title="Security Certification">
            This report summarizes the security, privacy, and integrity status of the current workflow.
          </Alert>

          {isLoadingReport ? (
            <Group justify="center" py="xl"><Loader size="md" /></Group>
          ) : reportError ? (
            <Alert color="red" icon={<IconAlertCircle size="1rem" />}>{reportError}</Alert>
          ) : (
            <>
              <Paper withBorder p="md" bg="gray.0">
                <ScrollArea.Autosize mah={400}>
                  <Text style={{ whiteSpace: 'pre-wrap', fontFamily: 'monospace', fontSize: '13px' }}>
                    {reportText}
                  </Text>
                </ScrollArea.Autosize>
              </Paper>

              <Group justify="flex-end">
                <Button variant="light" color="blue" onClick={() => downloadReport('text')}>
                  Download Text
                </Button>
                <Button color="orange" onClick={() => downloadReport('pdf')}>
                  Download PDF Report
                </Button>
              </Group>
            </>
          )}
        </Stack>
      </Modal>
    </>
  );
}


