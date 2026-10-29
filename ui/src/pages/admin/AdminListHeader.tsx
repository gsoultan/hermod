import type { ReactNode } from 'react';
import { PageHeader, SectionHeader } from '@/components/rows';

interface AdminListHeaderProps {
  title: string;
  lede: string;
  actions?: ReactNode;
  /** Shown as a tab of Settings › Workspace, which already says where you are. */
  embedded?: boolean;
  back: { to: string; label: string; search?: Record<string, string> };
}

/**
 * People, virtual hosts and workers open on their own and as tabs of Settings ›
 * Workspace. On their own they get the page header and the way back; inside
 * the tab a section heading, so the page does not carry two titles.
 */
export function AdminListHeader({ title, lede, actions, embedded, back }: AdminListHeaderProps) {
  if (embedded) return <SectionHeader title={title} lede={lede} actions={actions} />;
  return <PageHeader title={title} lede={lede} actions={actions} back={back} />;
}
