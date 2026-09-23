import { Group, Paper, Text, ThemeIcon } from '@mantine/core';

export interface StatCardProps {
  title: string;
  value: string | number;
  icon: React.ElementType;
  color: string;
  description?: string;
}

/**
 * A single metric.
 *
 * There is deliberately no trend badge. The previous version rendered a
 * hardcoded "+5%" whenever throughput was above zero — a number with no source
 * behind it, on the one screen whose entire job is to be believed.
 *
 * Lifted out of DashboardPage so ClusterResourceCards can use the same card
 * rather than growing a second one that drifts from it.
 */
export function StatCard({ title, value, icon: Icon, color, description }: StatCardProps) {
  return (
    <Paper
      withBorder
      p="md"
      radius="md"
      h="100%"
      style={{ display: 'flex', flexDirection: 'column', justifyContent: 'space-between' }}
    >
      <Group justify="space-between" wrap="nowrap">
        <div>
          <Text size="xs" c="dimmed" fw={700} style={{ textTransform: 'uppercase', letterSpacing: '0.5px' }}>
            {title}
          </Text>
          <Text size="xl" fw={800} mt={4}>
            {value}
          </Text>
        </div>
        <ThemeIcon color={color} variant="light" size="xl" radius="md">
          <Icon size="1.4rem" />
        </ThemeIcon>
      </Group>
      {description && (
        <Text size="xs" c="dimmed" mt="md">
          {description}
        </Text>
      )}
    </Paper>
  );
}
