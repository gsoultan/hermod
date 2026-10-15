import { Box, Group, Progress, Stack, Text } from '@mantine/core'

// The analysis' charts: one series each, in the theme's primary colour, so
// they read the same in light and dark mode. Each states what it shows in
// words (aria-label) and names every bar on hover (title), and the numbers
// they draw are also given as text beside them.

const pct = (share: number) => {
  const p = share * 100
  return p > 0 && p < 1 ? '<1%' : `${Math.round(p)}%`
}

/** A compact number: 1234567 is 1.23M, 0.000123 is 1.23e-4. */
export function short(n: number): string {
  const a = Math.abs(n)
  if (a !== 0 && (a < 0.001 || a >= 1e15)) return n.toExponential(2)
  if (a >= 1e9) return `${+(n / 1e9).toFixed(2)}B`
  if (a >= 1e6) return `${+(n / 1e6).toFixed(2)}M`
  if (a >= 1e4) return `${+(n / 1e3).toFixed(1)}k`
  return `${+n.toFixed(a < 10 ? 3 : 2)}`
}

export interface Bar {
  /** What the bar is, as the hover names it. */
  label: string
  rows: number
}

/**
 * Columns of rows, such as a histogram or rows per month, with the first and
 * last label under them.
 */
export function ColumnBars({ bars, label, from, to, height = 72 }: {
  bars: Bar[]
  /** What the chart shows, in words, for a screen reader. */
  label: string
  from: string
  to: string
  height?: number
}) {
  const most = Math.max(1, ...bars.map((b) => b.rows))
  return (
    <Stack gap={2}>
      <Box component="figure" m={0} aria-label={label} style={{ display: 'flex', alignItems: 'flex-end', gap: 2, height }}>
        {bars.map((b, i) => (
          <Box key={i} title={`${b.label}: ${b.rows.toLocaleString()} rows`} style={{
            flex: 1, minWidth: 2, height: `${b.rows ? Math.max(4, (b.rows / most) * 100) : 0}%`,
            background: 'var(--mantine-primary-color-filled)', borderRadius: '4px 4px 0 0',
          }} />
        ))}
      </Box>
      <Box style={{ borderTop: '1px solid var(--mantine-color-default-border)' }} />
      <Group justify="space-between" gap="xs" wrap="nowrap">
        <Text size="xs" c="dimmed">{from}</Text>
        <Text size="xs" c="dimmed">{to}</Text>
      </Group>
    </Stack>
  )
}

/** The most common values of a column, each a bar of its share, as a list. */
export function ShareBars({ items, other, label }: {
  items: Array<{ value: string; rows: number; share: number }>
  other?: number
  label: string
}) {
  const shown = items.slice(0, 6)
  return (
    <Stack gap={6} component="ul" aria-label={label} style={{ listStyle: 'none', margin: 0, padding: 0 }}>
      {shown.map((t) => (
        <Box component="li" key={t.value}>
          <Group justify="space-between" gap="xs" wrap="nowrap">
            <Text size="xs" truncate="end" title={t.value} style={{ minWidth: 0 }}>{t.value === '' ? '(blank)' : t.value}</Text>
            <Text size="xs" c="dimmed" style={{ whiteSpace: 'nowrap' }}>{pct(t.share)}</Text>
          </Group>
          <Progress value={t.share * 100} size="sm" radius="xl" aria-hidden />
        </Box>
      ))}
      {(other ?? 0) > 0 && (
        <Box component="li">
          <Group justify="space-between" gap="xs" wrap="nowrap">
            <Text size="xs" c="dimmed">All other values</Text>
            <Text size="xs" c="dimmed">{pct(other ?? 0)}</Text>
          </Group>
        </Box>
      )}
    </Stack>
  )
}

/** How strongly two columns move together, -1 to 1, as a bar from the middle. */
export function StrengthBar({ r }: { r: number }) {
  const width = Math.abs(r) * 50
  return (
    <Box aria-hidden style={{ position: 'relative', height: 8, borderRadius: 4, background: 'var(--mantine-color-default-border)' }}>
      <Box style={{
        position: 'absolute', top: 0, bottom: 0, borderRadius: 4, background: 'var(--mantine-primary-color-filled)',
        left: r >= 0 ? '50%' : `${50 - width}%`, width: `${width}%`,
      }} />
    </Box>
  )
}
