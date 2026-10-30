import { Group, Text, ThemeIcon } from '@mantine/core';
import { IconAlertTriangle, IconCircleCheck, IconLoader2 } from '@tabler/icons-react';
import type { ReactNode } from 'react';

/** The backend's reason as a sentence: capitalised, with a full stop. */
export const sentence = (text: string) => {
  const t = text.trim();
  if (!t) return t;
  return t[0].toUpperCase() + t.slice(1) + (/[.!?]$/.test(t) ? '' : '.');
};

/** One line of a readiness check: what is fine, what is not, what is still being asked. */
export function ReadinessCheck({ state, children }: { state: 'ok' | 'problem' | 'waiting'; children: ReactNode }) {
  const icon =
    state === 'ok' ? (
      <ThemeIcon color="green" variant="light" size="sm" radius="xl">
        <IconCircleCheck size="0.9rem" />
      </ThemeIcon>
    ) : state === 'problem' ? (
      <ThemeIcon color="orange" variant="light" size="sm" radius="xl">
        <IconAlertTriangle size="0.9rem" />
      </ThemeIcon>
    ) : (
      <ThemeIcon color="gray" variant="light" size="sm" radius="xl">
        <IconLoader2 size="0.9rem" />
      </ThemeIcon>
    );
  return (
    <Group gap="xs" wrap="nowrap" align="flex-start">
      {icon}
      <Text size="sm" style={{ flex: 1 }}>
        {children}
      </Text>
    </Group>
  );
}
