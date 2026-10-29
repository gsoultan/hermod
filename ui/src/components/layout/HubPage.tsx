import { Box, Center, Loader, ScrollArea, Stack, Tabs } from '@mantine/core';
import { PageHeader } from '@/components/rows';
import { Suspense, type ReactNode } from 'react';
import type { HubTab } from './nav';

interface HubPageProps {
  title: string;
  lede?: string;
  tabs: HubTab[];
  value: string;
  onChange: (tab: string) => void;
  /** The one button for the open tab, such as Add source, on the right of the title. */
  actions?: ReactNode;
  children: ReactNode;
}

/**
 * One of the five places, shown as a title and a row of tabs. Each tab hosts a
 * page that used to be its own sidebar entry; the page itself is unchanged, so
 * nothing it could do is lost, only where it is reached from.
 */
export function HubPage({ title, lede, tabs, value, onChange, actions, children }: HubPageProps) {
  const current = tabs.some((t) => t.value === value) ? value : tabs[0]?.value;
  return (
    <Stack gap={0}>
      <PageHeader title={title} lede={lede} actions={actions} />
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
