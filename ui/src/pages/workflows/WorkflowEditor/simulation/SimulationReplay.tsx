import { useEffect } from 'react';
import { ActionIcon, Button, Group, Text, Tooltip } from '@mantine/core';
import { IconPlayerPauseFilled, IconPlayerPlayFilled, IconPlayerSkipBack, IconPlayerSkipForward } from '@tabler/icons-react';
import { useWorkflowStore } from '../store/useWorkflowStore';
import { simulationHopCount } from './simulationPath';

/** How long one hop of a replay takes at 1×. */
export const REPLAY_HOP_MS = 600;

const SPEEDS = [0.5, 1, 2] as const;

const prefersReducedMotion = () =>
  typeof window !== 'undefined' && !!window.matchMedia?.('(prefers-reduced-motion: reduce)')?.matches;

/**
 * Replays the last simulation hop by hop, and the controls for it. A run asked
 * for from the editor plays once and then settles on the whole path; with
 * reduced motion it goes straight to the settled path.
 */
export function SimulationReplay() {
  const total = useWorkflowStore((s) => simulationHopCount(s.testResults, s.testRunGraph));
  const replay = useWorkflowStore((s) => s.simReplay);
  const setSimReplay = useWorkflowStore((s) => s.setSimReplay);
  const speed = useWorkflowStore((s) => s.replaySpeed);
  const setReplaySpeed = useWorkflowStore((s) => s.setReplaySpeed);

  useEffect(() => {
    if (!replay?.playing) return;
    if (total === 0 || prefersReducedMotion()) {
      setSimReplay(null);
      return;
    }
    // Hop 0 is the moment before the first edge is crossed.
    const wait = replay.hop === 0 ? 0 : REPLAY_HOP_MS / speed;
    const timer = setTimeout(() => {
      const next = replay.hop + 1;
      setSimReplay(next > total ? null : { ...replay, hop: next });
    }, wait);
    return () => clearTimeout(timer);
  }, [replay, total, speed, setSimReplay]);

  if (total === 0) return null;

  const hop = replay ? Math.max(replay.hop, 1) : total;
  const playing = !!replay?.playing;

  const stepTo = (next: number) =>
    setSimReplay(next >= total + 1 ? null : { hop: Math.max(1, next), playing: false });

  return (
    <Group gap={4} wrap="nowrap" aria-label="Replay">
      <Tooltip label="Step back" withArrow>
        <ActionIcon size="sm" variant="subtle" color="gray" aria-label="Step back" disabled={hop <= 1} onClick={() => stepTo(hop - 1)}>
          <IconPlayerSkipBack size={14} aria-hidden />
        </ActionIcon>
      </Tooltip>
      <Button
        size="compact-sm"
        variant="light"
        color="green"
        leftSection={playing ? <IconPlayerPauseFilled size={12} aria-hidden /> : <IconPlayerPlayFilled size={12} aria-hidden />}
        onClick={() =>
          setSimReplay(playing ? { hop, playing: false } : { hop: replay && hop < total ? hop : 0, playing: true })
        }
      >
        {playing ? 'Pause' : 'Replay'}
      </Button>
      <Tooltip label="Step forward" withArrow>
        <ActionIcon size="sm" variant="subtle" color="gray" aria-label="Step forward" disabled={!replay} onClick={() => stepTo(hop + 1)}>
          <IconPlayerSkipForward size={14} aria-hidden />
        </ActionIcon>
      </Tooltip>
      <Text size="xs" c="dimmed" style={{ fontVariantNumeric: 'tabular-nums' }} aria-live="polite">
        hop {hop} / {total}
      </Text>
      <Tooltip label="Replay speed" withArrow>
        <Button
          size="compact-xs"
          variant="subtle"
          color="gray"
          aria-label={`Replay speed ${speed}×`}
          onClick={() => {
            setReplaySpeed(SPEEDS[(SPEEDS.indexOf(speed as (typeof SPEEDS)[number]) + 1) % SPEEDS.length]);
          }}
        >
          {speed}×
        </Button>
      </Tooltip>
    </Group>
  );
}
