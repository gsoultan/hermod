import { Badge, Select, Text } from '@mantine/core';
import { IconFolder } from '@tabler/icons-react';
import { useQuery } from '@tanstack/react-query';
import type { Workspace } from '@/types';
import { apiFetch } from '@/api';

/**
 * The workspace list, shared by every screen that names one.
 *
 * /api/workspaces answers with a bare array, but an error envelope would be an
 * object — .map on it is an uncaught render crash — so the array check lives
 * here once instead of at each call site.
 */
export function useWorkspaces(): Workspace[] {
  const { data } = useQuery<Workspace[]>({
    queryKey: ['workspaces'],
    queryFn: async () => {
      const res = await apiFetch('/api/workspaces');
      if (!res.ok) return [];
      return res.json();
    },
    staleTime: 60_000,
  });
  return Array.isArray(data) ? data : [];
}

/** The options for a "filter by workspace" control, including the all/none rows. */
export function useWorkspaceFilterOptions() {
  const workspaces = useWorkspaces();
  return [
    { value: 'all', label: 'All Workspaces' },
    { value: 'none', label: 'No workspace' },
    ...workspaces.map((ws) => ({ value: ws.id, label: ws.name })),
  ];
}

/**
 * A row's workspace, by name.
 *
 * Falling back to the raw id is deliberate: it is how a reference to a deleted
 * workspace used to show up, and keeping it visible beats rendering nothing
 * where a name belongs.
 */
export function WorkspaceBadge({ id, workspaces }: { id?: string; workspaces: Workspace[] }) {
  if (!id) return <Text size="sm" c="dimmed">Default</Text>;
  const ws = workspaces.find((w) => w.id === id);
  return (
    <Badge variant="light" color={ws ? 'blue' : 'gray'} leftSection={<IconFolder size="0.7rem" />}>
      {ws ? ws.name : id}
    </Badge>
  );
}

/**
 * The workspace picker, shared by the source and sink forms.
 *
 * Sources and sinks have carried workspace_id in the schema and indexed it
 * since workspaces were added, but nothing in the UI ever set it — only
 * workflows had a picker, buried in the editor's settings drawer. One component
 * rather than two copies, so the two forms cannot drift.
 *
 * The list arrives as a prop rather than being fetched here, so the wizards
 * stay presentational — they render under a bare MantineProvider in tests, and
 * a useQuery inside a leaf would make every one of them need a QueryClient.
 * It is also the pattern vhosts and workers already follow: the form fetches,
 * the wizard renders.
 *
 * Unassigned is a real choice, so the field is clearable and an empty value
 * means "no workspace" rather than "not answered yet".
 */
export function WorkspaceSelect({
  value,
  onChange,
  workspaces = [],
  label = 'Workspace (Optional)',
  description = 'Groups this connection and counts against the workspace quota',
  ...rest
}: {
  value: string | undefined;
  onChange: (workspaceID: string) => void;
  workspaces?: Workspace[];
  label?: string;
  description?: string;
} & Omit<React.ComponentProps<typeof Select>, 'value' | 'onChange' | 'data' | 'label' | 'description'>) {
  return (
    <Select
      label={label}
      description={description}
      placeholder="No workspace"
      data={workspaces.map((ws) => ({ value: ws.id, label: ws.name }))}
      value={value || null}
      // Mantine types a Select's value as widely as its data allows; the ids
      // here are strings, and clearing yields null, which is the "unassigned"
      // case the empty string stands for.
      onChange={(val) => onChange(val ? String(val) : '')}
      clearable
      leftSection={<IconFolder size="0.9rem" />}
      mih={80}
      {...rest}
    />
  );
}
