import { Box, Center, Loader, ScrollArea, Stack, Tabs, Text, Title } from '@mantine/core';
import { Suspense, type ReactNode } from 'react';
import type { HubTab } from './nav';

interface HubPageProps {
  title: string;
  lede?: string;
  tabs: HubTab[];
  value: string;
  onChange: (tab: string) => void;
  children: ReactNode;
}

/**
 * One of the five places, shown as a title and a row of tabs. Each tab hosts a
 * page that used to be its own sidebar entry; the page itself is unchanged, so
 * nothing it could do is lost, only where it is reached from.
 */
export function HubPage({ title, lede, tabs, value, onChange, children }: HubPageProps) {
  const current = tabs.some((t) => t.value === value) ? value : tabs[0]?.value;
  return (
    <Stack gap="md">
      <Box>
        <Title order={1} size="h2">{title}</Title>
        {lede && <Text c="dimmed" size="sm">{lede}</Text>}
      </Box>
      <Tabs value={current} onChange={(t) => t && onChange(t)} keepMounted={false}>
        <ScrollArea type="never" scrollbars="x">
          <Tabs.List style={{ flexWrap: 'nowrap' }}>
            {tabs.map((t) => (
              <Tabs.Tab key={t.value} value={t.value} style={{ whiteSpace: 'nowrap' }}>{t.label}</Tabs.Tab>
            ))}
          </Tabs.List>
        </ScrollArea>
        <Box pt="md">
          <Suspense fallback={<Center h={200}><Loader /></Center>}>
            {children}
          </Suspense>
        </Box>
      </Tabs>
    </Stack>
  );
}
