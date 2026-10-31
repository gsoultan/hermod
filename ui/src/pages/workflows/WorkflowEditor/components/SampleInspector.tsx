import { useState } from 'react';
import { Alert, Button, Group, JsonInput, Modal, SegmentedControl, Stack, Text } from '@mantine/core';
import { IconInfoCircle } from '@tabler/icons-react';
import { useShallow } from 'zustand/react/shallow';
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore';
import { latestRecord } from '../nodes/latestRecord';

/**
 * The latest record a step processed on a live run, opened from the eye on its
 * card. The editor and the read-only view share it: both fill the same store
 * from the workflow's status feed.
 */
export function SampleInspector() {
  const { opened, nodeId, nodeSamples, nodes, setOpened } = useWorkflowStore(useShallow((state) => ({
    opened: state.sampleInspectorOpened,
    nodeId: state.sampleNodeId,
    nodeSamples: state.nodeSamples,
    nodes: state.nodes,
    setOpened: state.setSampleInspectorOpened,
  })));

  // It leads with the record; the whole message (id, operation, table,
  // metadata) is one switch away.
  const [view, setView] = useState<'record' | 'message'>('record');
  const sample = nodeId ? nodeSamples[nodeId] : undefined;
  const isMessage = sample != null && latestRecord(sample) !== (Array.isArray(sample) ? sample[sample.length - 1] : sample);
  const stepName = (nodes.find((n: any) => n.id === nodeId)?.data as any)?.label || 'this step';
  const showMessage = view === 'message' && isMessage;

  return (
    <Modal
      opened={opened}
      onClose={() => setOpened(false)}
      title={<Text fw={700}>Latest record</Text>}
      size="lg"
    >
      <Stack>
        <Text size="sm" c="dimmed">
          The newest record <b>{stepName}</b> passed on. It updates while the flow runs.
        </Text>
        {sample != null ? (
          <>
            {isMessage && (
              <SegmentedControl
                size="xs"
                value={view}
                onChange={(v) => setView(v as 'record' | 'message')}
                data={[{ value: 'record', label: 'Record' }, { value: 'message', label: 'Whole message' }]}
                style={{ alignSelf: 'flex-start' }}
              />
            )}
            <JsonInput
              aria-label={showMessage ? 'Whole message' : 'Record'}
              value={JSON.stringify(showMessage ? sample : latestRecord(sample), null, 2)}
              readOnly
              autosize
              minRows={6}
              maxRows={20}
              styles={{ input: { fontFamily: 'monospace', fontSize: '13px' } }}
            />
          </>
        ) : (
          <Alert icon={<IconInfoCircle size="1rem" />} color="blue">
            No record has passed this step yet. Turn the flow on to see live data.
          </Alert>
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={() => setOpened(false)}>Close</Button>
        </Group>
      </Stack>
    </Modal>
  );
}
