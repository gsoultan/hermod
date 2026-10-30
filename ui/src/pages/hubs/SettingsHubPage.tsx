import { getSessionRole } from '@/auth/session';
import { settingsRows } from '@/components/layout/nav';
import { PageHeader, Row, RowList } from '@/components/rows';

/**
 * Settings as one list. Each row is a place that used to be its own sidebar
 * link or a tab of the platform settings; the row says what is inside so
 * nobody has to open it to find out.
 */
export function SettingsHubPage() {
  const rows = settingsRows(getSessionRole());
  return (
    <>
      <PageHeader title="Settings" lede="Your workspace, the people in it, and how Hermod behaves." />
      <RowList aria-label="Settings">
        {rows.map((row) => (
          <Row key={row.key} title={row.label} detail={row.detail} to={row.search ? `${row.to}?${new URLSearchParams(row.search)}` : row.to} />
        ))}
      </RowList>
    </>
  );
}
