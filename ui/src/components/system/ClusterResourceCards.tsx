import { Grid } from '@mantine/core';
import { IconCpu, IconDatabase, IconDeviceSdCard } from '@tabler/icons-react';
import { StatCard } from './StatCard';
import type { DashboardStats } from '@/hooks/useDashboardStream';
import { NO_READING, formatBytes, formatPercent, usageFraction } from '@/utils/metricFormat';

/**
 * The colour a resource card takes as the resource runs out.
 *
 * An unknown reading is grey rather than green: "we do not know" must not look
 * like "all clear" on a screen whose job is to be believed.
 */
function pressureColor(fraction: number | null): string {
  if (fraction === null) return 'gray';
  if (fraction >= 0.9) return 'red';
  if (fraction >= 0.75) return 'orange';
  return 'teal';
}

/** "48 GB used · 50%", or just the size when there is no share to quote. */
function usedDescription(used: number, fraction: number | null): string {
  const size = formatBytes(used);
  if (fraction === null) return 'No online worker reported a size';
  if (size === NO_READING) return `${formatPercent(fraction)} in use`;
  return `${size} used · ${formatPercent(fraction)}`;
}

/**
 * What the platform is running on: cores, memory and disk, summed over the
 * workers that are currently online.
 *
 * The dashboard could say how many workers were up and nothing whatsoever
 * about what those workers were — no core count, no memory, no disk. "Two
 * workers online" is not an answer to "can this cluster take the load", and it
 * is the same answer for two laptops and two 64-core servers.
 *
 * Every card renders an absent reading as an em-dash rather than a zero. Zero
 * cores is not a machine, it is a worker on a release from before capacity
 * reporting, or no online worker at all — see storage.WorkerResources.
 */
export function ClusterResourceCards({ stats }: { stats: DashboardStats }) {
  const cpuKnown = stats.cpu_cores > 0;
  const memFraction = usageFraction(stats.memory_used_bytes, stats.memory_total_bytes);
  const storageFraction = usageFraction(stats.storage_used_bytes, stats.storage_total_bytes);

  return (
    <Grid grow gap="sm">
      <Grid.Col span={{ base: 12, sm: 6, md: 4 }}>
        <StatCard
          title="CPU Cores"
          value={cpuKnown ? stats.cpu_cores : NO_READING}
          icon={IconCpu}
          color={pressureColor(cpuKnown ? stats.cpu_usage : null)}
          description={
            cpuKnown
              ? `${formatPercent(stats.cpu_usage)} in use across ${stats.active_workers} worker${stats.active_workers === 1 ? '' : 's'}`
              : 'No online worker reported a core count'
          }
        />
      </Grid.Col>
      <Grid.Col span={{ base: 12, sm: 6, md: 4 }}>
        <StatCard
          title="Memory"
          value={formatBytes(stats.memory_total_bytes)}
          icon={IconDatabase}
          color={pressureColor(memFraction)}
          description={usedDescription(stats.memory_used_bytes, memFraction)}
        />
      </Grid.Col>
      <Grid.Col span={{ base: 12, sm: 6, md: 4 }}>
        <StatCard
          title="Storage"
          value={formatBytes(stats.storage_total_bytes)}
          icon={IconDeviceSdCard}
          color={pressureColor(storageFraction)}
          description={usedDescription(stats.storage_used_bytes, storageFraction)}
        />
      </Grid.Col>
    </Grid>
  );
}
