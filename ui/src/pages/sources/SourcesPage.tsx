import { ConnectionsList } from '@/components/connections/ConnectionsList';

/** The Sources tab of Connections: one row per source. */
export function SourcesPage() {
  return <ConnectionsList kind="source" />;
}
