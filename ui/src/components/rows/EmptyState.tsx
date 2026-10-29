import { Button, Stack, Text } from '@mantine/core';
import { Link } from '@tanstack/react-router';

interface EmptyStateProps {
  title: string;
  detail?: string;
  /** Exactly one thing to do next. */
  action?: { label: string; onClick?: () => void; to?: string };
}

/** An empty list says what the list is for and offers one button. */
export function EmptyState({ title, detail, action }: EmptyStateProps) {
  return (
    <Stack align="center" gap="xs" py={48} px="md" ta="center">
      <Text fw={600}>{title}</Text>
      {detail && <Text size="sm" c="dimmed" maw={420}>{detail}</Text>}
      {action && (action.to
        ? <Button component={Link} to={action.to} mt="sm">{action.label}</Button>
        : <Button onClick={action.onClick} mt="sm">{action.label}</Button>)}
    </Stack>
  );
}
