import { ConnectionsList } from '@/components/connections/ConnectionsList';

/** The Sinks tab of Connections: one row per sink. */
export function SinksPage() {
  return <ConnectionsList kind="sink" />;
}
