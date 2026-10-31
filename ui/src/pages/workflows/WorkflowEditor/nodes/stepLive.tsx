import type { ReactNode } from 'react';
import { Badge, Text, UnstyledButton } from '@mantine/core';
import { IconEye } from '@tabler/icons-react';
import { useShallow } from 'zustand/react/shallow';
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore';
import { nodeSimulationResult } from '@/pages/workflows/WorkflowEditor/simulation/simulationPath';
import { nodeLiveProblem } from '@/pages/workflows/WorkflowEditor/live/liveHealth';
import { latestRecord } from './latestRecord';

/**
 * What a step card shows of a live run: its counts, the newest record it
 * passed on and anything wrong with it. The editor's cards (BaseNode) and the
 * read-only view's (DetailFlowCanvas) both draw from here, so a running flow
 * reads the same on either page.
 */

/** The newest record a step has seen, on one line. */
export function samplePreview(sample: unknown): string {
  const latest = latestRecord(sample);
  if (typeof latest === 'string') return latest;
  try {
    return JSON.stringify(latest) ?? String(latest);
  } catch {
    return String(latest);
  }
}

function problemFields(p: ReturnType<typeof nodeLiveProblem>) {
  return {
    problemSeverity: p?.severity ?? null,
    problemText: p ? p.text.charAt(0).toUpperCase() + p.text.slice(1) : null,
  };
}

/**
 * One step's live telemetry, from the store the status feed fills, falling
 * back to what the node's own data carries.
 *
 * One shallow subscription instead of eight. Zustand evaluates every selector
 * on every set(), and the store is written on every telemetry frame, so
 * selecting the scalars together under useShallow means one comparison per
 * node, and a re-render only when this node's own numbers move.
 */
export function useStepLive(id: string, data: any) {
  const live = useWorkflowStore(
    useShallow(state => ({
      metric: state.nodeMetrics[id],
      errorCount: state.nodeErrorMetrics[id],
      sample: state.nodeSamples[id],
      cbStatus: state.sinkCBStatuses[data.ref_id],
      bufferFill: state.sinkBufferFill[data.ref_id],
      sourceStatus: state.sourceStatus,
      sinkStatus: state.sinkStatuses[data.ref_id],
      // The same object until the next run, so a telemetry frame does not
      // re-render the node on its account.
      simulation: nodeSimulationResult(state.testResults, id),
      // What is wrong here on a live run, as plain fields so useShallow
      // compares them; null fields when nothing is.
      ...problemFields(state.testResults ? null : nodeLiveProblem(state, id)),
    }))
  );
  return {
    ...live,
    metric: live.metric ?? data.metric,
    errorCount: live.errorCount ?? data.errorCount,
    sample: live.sample ?? data.sample,
    cbStatus: live.cbStatus ?? data.cbStatus,
    bufferFill: live.bufferFill ?? data.bufferFill,
    problemColor: live.problemSeverity === 'error' ? 'red' : live.problemSeverity === 'warning' ? 'orange' : null,
  };
}

/** What the step has done and anything wrong with it, as one line in the card. */
export function stepStats({ cbStatus, metric, errorCount, dlqCount, bufferFill }: {
  cbStatus?: string; metric?: number; errorCount?: number; dlqCount?: number; bufferFill?: number;
}): ReactNode[] {
  const stats: ReactNode[] = [];
  if (cbStatus === 'open') stats.push(<Badge key="cb" size="xs" variant="light" color="red">Circuit open</Badge>);
  if (cbStatus === 'half-open') stats.push(<Badge key="cb" size="xs" variant="light" color="orange">Circuit half-open</Badge>);
  if (metric! > 0) stats.push(<Text key="m" size="xs" c="dimmed">{`${metric!.toLocaleString()} processed`}</Text>);
  if (errorCount! > 0) stats.push(<Text key="e" size="xs" c="red.7">{`${errorCount!.toLocaleString()} ${errorCount === 1 ? 'error' : 'errors'}`}</Text>);
  if (dlqCount! > 0) stats.push(<Text key="d" size="xs" c="orange.7">{`${dlqCount!.toLocaleString()} failed`}</Text>);
  if (bufferFill! > 0) {
    stats.push(
      <Text key="b" size="xs" c={bufferFill! > 0.8 ? 'red.7' : 'dimmed'}>{`Buffer ${Math.round(bufferFill! * 100)}%`}</Text>,
    );
  }
  return stats;
}

/** The newest record on one line, behind the eye; a click opens it in full. */
export function StepSample({ nodeId, sample }: { nodeId: string; sample: unknown }) {
  const open = useWorkflowStore(useShallow(state => ({
    setSampleNodeId: state.setSampleNodeId,
    setSampleInspectorOpened: state.setSampleInspectorOpened,
  })));
  return (
    <UnstyledButton
      className="hermod-step-sample nodrag"
      aria-label="Latest record, open in full"
      onClick={(e) => {
        e.stopPropagation();
        open.setSampleNodeId(nodeId);
        open.setSampleInspectorOpened(true);
      }}
    >
      <IconEye size="0.85rem" aria-hidden />
      <Text component="span" size="xs" ff="monospace" truncate>{samplePreview(sample)}</Text>
    </UnstyledButton>
  );
}

/** On a live run, what is wrong here, above the card where it is seen first. */
export function StepProblem({ color, text }: { color: string; text: string }) {
  return (
    <Badge
      role="note"
      aria-label={`Problem: ${text}`}
      color={color}
      variant="filled"
      size="sm"
      radius="sm"
      style={{ position: 'absolute', top: -12, left: 10, zIndex: 105, textTransform: 'none', maxWidth: 'calc(100% - 20px)' }}
    >
      {text}
    </Badge>
  );
}
