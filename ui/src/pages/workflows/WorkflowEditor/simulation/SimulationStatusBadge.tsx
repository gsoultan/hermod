import { ThemeIcon, Tooltip, VisuallyHidden } from '@mantine/core';
import { IconCheck, IconFilterOff, IconMinus, IconX, type TablerIcon } from '@tabler/icons-react';
import { describeSimulationResult, type SimulationNodeResult, type SimulationNodeStatus } from './simulationPath';

/**
 * How each outcome looks. The node's ring, its badge and the summary share one
 * colour per outcome, and the icon carries the outcome too, so it does not rest
 * on colour alone.
 */
export const SIMULATION_STATUS_STYLE: Record<SimulationNodeStatus, { color: string; icon: TablerIcon }> = {
  passed: { color: 'green', icon: IconCheck },
  filtered: { color: 'yellow', icon: IconFilterOff },
  error: { color: 'red', icon: IconX },
  skipped: { color: 'gray', icon: IconMinus },
};

/** The corner badge that says what the last simulation did to a node. */
export function SimulationStatusBadge({ result }: { result: SimulationNodeResult }) {
  const { color, icon: Icon } = SIMULATION_STATUS_STYLE[result.status];
  const description = describeSimulationResult(result);
  return (
    <Tooltip label={description} withArrow multiline maw={320} position="top">
      <ThemeIcon
        color={color}
        // Picks a dark or light icon per colour, so each one stays legible on
        // its own fill -- a white tick on yellow would not be.
        autoContrast
        radius="xl"
        size={22}
        style={{
          position: 'absolute',
          top: -11,
          right: -11,
          zIndex: 105,
          border: '2px solid var(--mantine-color-body)',
          boxShadow: '0 2px 4px rgba(0,0,0,0.2)',
        }}
      >
        <Icon size={13} stroke={3} aria-hidden />
        {/* Read out as part of the node, since the tooltip needs a pointer. */}
        <VisuallyHidden>{description}</VisuallyHidden>
      </ThemeIcon>
    </Tooltip>
  );
}
