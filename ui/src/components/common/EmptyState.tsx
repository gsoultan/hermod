import { Button, Center, Stack, Text, ThemeIcon, rem } from '@mantine/core';
import { IconInbox } from '@tabler/icons-react';
import type { ReactNode } from 'react';

interface EmptyStateProps {
  /** What is missing, in the user's words: "No workers registered". */
  title: string;
  /** Why it might be empty and what to do about it. One sentence. */
  description?: ReactNode;
  icon?: ReactNode;
  /** The single most useful next step, if there is one. */
  action?: { label: string; onClick: () => void };
  compact?: boolean;
}

/**
 * A blank table reads as "this page is broken", not "there is nothing here
 * yet". This says which is true, and offers the obvious next step.
 *
 * Deliberately one component rather than per-page markup, so every list in the
 * app explains itself the same way.
 */
export function EmptyState({ title, description, icon, action, compact }: EmptyStateProps) {
  return (
    <Center py={compact ? 'lg' : 'xl'}>
      <Stack align="center" gap="xs" maw={420}>
        <ThemeIcon variant="light" color="gray" size={compact ? 40 : 56} radius="xl">
          {icon ?? <IconInbox style={{ width: rem(compact ? 20 : 28), height: rem(compact ? 20 : 28) }} />}
        </ThemeIcon>
        <Text fw={600} size={compact ? 'sm' : 'md'} ta="center">
          {title}
        </Text>
        {description && (
          <Text size="sm" c="dimmed" ta="center">
            {description}
          </Text>
        )}
        {action && (
          <Button mt="xs" size="sm" variant="light" onClick={action.onClick}>
            {action.label}
          </Button>
        )}
      </Stack>
    </Center>
  );
}
