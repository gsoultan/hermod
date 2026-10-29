import { useNavigate, useSearch } from '@tanstack/react-router';
import type { HubTab } from '@/components/layout/nav';

/**
 * The open tab of a hub lives in the URL (?tab=), so a link can open a hub on
 * one of its pages and the back button returns to the previous tab.
 */
export function useHubTab(tabs: HubTab[]): [string, (tab: string) => void] {
  const search = useSearch({ strict: false }) as Record<string, unknown>;
  const navigate = useNavigate();
  const requested = typeof search.tab === 'string' ? search.tab : '';
  const value = tabs.some((t) => t.value === requested) ? requested : tabs[0]?.value ?? '';
  const setTab = (tab: string) => {
    navigate({ to: '.', search: (prev: Record<string, unknown>) => ({ ...prev, tab }) } as any);
  };
  return [value, setTab];
}
