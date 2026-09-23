import { Center, Group, RingProgress, Stack, Text, Tooltip } from '@mantine/core';
import { NO_READING } from '@/utils/metricFormat';

export interface ResourceGaugeProps {
  /**
   * What is being measured — "CPU", "Memory", "Storage".
   *
   * Optional, and omitted inside a table whose column header already says it.
   * Three repeated labels cost about 200px of row width, which is what pushed
   * the workers table's Description column off a 1440px screen.
   */
  label?: string;
  /** Share in use, 0..1, or null when the worker reported no reading. */
  fraction: number | null;
  /** The capacity the fraction is a share of — "8 cores", "16 / 32 GB". */
  caption: string;
  /** Long form for the hover, since the cell itself has to stay narrow. */
  tooltip: string;
}

/**
 * The colour a resource turns as it fills.
 *
 * Three bands rather than the two the workers page used, because the two were
 * "fine" and "over 80%", and the gap between a disk at 82% and one at 98% is
 * the difference between next week's problem and tonight's.
 */
function pressureColor(fraction: number): string {
  if (fraction >= 0.9) return 'red';
  if (fraction >= 0.75) return 'orange';
  return 'blue';
}

/**
 * One resource reading: how much of it is in use, and how much there is.
 *
 * The capacity underneath is the point. A ring alone says "80%", which is the
 * same reading on a two-core machine and a sixty-four-core one — and the
 * workers page showed exactly that, twice, with no sizes anywhere and no disk
 * reading at all.
 *
 * A null fraction draws a grey, empty ring with an em-dash in it rather than a
 * ring at 0%. A 0% ring says the worker answered and answered "nothing", which
 * for a worker on a release from before capacity reporting is a claim about a
 * machine nobody measured.
 */
export function ResourceGauge({ label, fraction, caption, tooltip }: ResourceGaugeProps) {
  const known = fraction !== null;
  const pct = known ? Math.round(fraction * 100) : 0;

  return (
    <Group gap="xs" wrap="nowrap">
      <Tooltip label={known ? `${tooltip}: ${pct}% of ${caption}` : `${tooltip}: not reported`}>
        {/* 54px, not 40. The percentage sits inside the ring, and at 40px it
            only fit by overriding the font down to 8px — below the 11px floor
            the layout audit enforces, and unreadable on a laptop screen. */}
        <RingProgress
          size={54}
          thickness={5}
          roundCaps
          sections={known ? [{ value: pct, color: pressureColor(fraction) }] : []}
          label={
            <Center>
              <Text size="xs" fw={700} c={known ? undefined : 'dimmed'}>
                {known ? `${pct}%` : NO_READING}
              </Text>
            </Center>
          }
        />
      </Tooltip>
      {/* Both lines are nowrap. In a table cell they are otherwise free to
          wrap, and "783.4 / 926.3 GB" broke across three lines — which trebles
          the row height and turns a capacity into a puzzle. The table already
          scrolls horizontally, so the column can be as wide as its content. */}
      <Stack gap={0} style={{ whiteSpace: 'nowrap' }}>
        {label && (
          <Text size="xs" c="dimmed">
            {label}
          </Text>
        )}
        <Text size="xs" fw={500}>
          {caption}
        </Text>
      </Stack>
    </Group>
  );
}
