import { lazy, Suspense, useState } from 'react';
import {
  ActionIcon, Badge, Box, Button, Checkbox, Group, Menu, Modal, Pagination, Select, Text,
} from '@mantine/core';
import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate } from '@tanstack/react-router';
import { useSessionStore } from '@/auth/session';
import { stashDraftWorkflow } from '@/lib/draftWorkflow';
import type { Workflow, Worker, Workspace } from '@/types';
import { apiFetch } from '@/api';
import { downloadWorkflowExport } from '@/utils/workflowExport';
import { notifications } from '@mantine/notifications';
import { useDebouncedValue, useDisclosure } from '@mantine/hooks';
import { useVHost } from '@/context/VHostContext';
import {
  IconChevronDown, IconCopy, IconDotsVertical, IconDownload, IconEdit, IconFolder, IconHierarchy, IconPlus,
  IconSparkles, IconTrash,
} from '@tabler/icons-react';
import { useConfirm } from '@/components/common/ConfirmProvider';
import { EmptyState, FilterBar, PageHeader, RowList } from '@/components/rows';
import { FlowRow } from '@/components/flows/FlowRow';
import { FLOW_FILTERS, fromTo, matchesFilter, type FlowFilter } from './flowsModel';
const API_BASE = '/api';

const TemplatesModal = lazy(() => import('./WorkflowsPage_TemplatesModal'))
// The wizard reaches every source, sink and transformation config form.
// Loading that with the workflow list would pay for it on every visit.
const ImportWizard = lazy(() =>
  import('@/components/workflow/Import/ImportWizard').then((m) => ({ default: m.ImportWizard })),
)
const DescribeAutomationModal = lazy(() =>
  import('./builder/DescribeAutomationModal').then((m) => ({ default: m.DescribeAutomationModal })),
)

/**
 * Flows: search, one filter, a workspace picker, and a row per flow with its
 * switch. Everything else about a flow is behind its ⋯ or the arrow into it;
 * everything about making one is behind New flow ▾.
 */
export default function WorkflowsPage() {
  const confirm = useConfirm();
  const queryClient = useQueryClient();
  const { selectedVHost, availableVHosts } = useVHost();
  const [search, setSearch] = useState('');
  // Debounced so a burst of keystrokes costs one request, and used as the query
  // key so the key changes once per search rather than once per character.
  const [debouncedSearch] = useDebouncedValue(search, 300);
  const [filter, setFilter] = useState<FlowFilter>('all');
  const [activePage, setPage] = useState(1);
  const itemsPerPage = 30;
  const [selectedWorkspace, setSelectedWorkspace] = useState<string>('all');
  const [selectedIDs, setSelectedIDs] = useState<string[]>([]);
  // The selection belongs to the rows on screen. Kept across a page, search,
  // workspace or vhost change, a batch delete reached workflows the user could
  // no longer see. Reset during render, so no frame shows the old count.
  const selectionScope = `${activePage}|${debouncedSearch}|${selectedVHost}|${selectedWorkspace}|${filter}`;
  const [selectionScopeSeen, setSelectionScopeSeen] = useState(selectionScope);
  if (selectionScope !== selectionScopeSeen) {
    setSelectionScopeSeen(selectionScope);
    setSelectedIDs([]);
  }
  const [importOpened, { open: openImport, close: closeImport }] = useDisclosure(false);
  const [templatesOpened, { open: openTemplates, close: closeTemplates }] = useDisclosure(false);
  const [describeOpened, { open: openDescribe, close: closeDescribe }] = useDisclosure(false);
  const navigate = useNavigate();
  // Drafting spends model credit and is meant to be saved, so the server
  // admits Editors and Administrators only.
  const role = useSessionStore((s) => s.user?.role);
  const canEdit = role === 'Administrator' || role === 'Editor';

  const { data: workspacesResponse } = useQuery<Workspace[]>({
    queryKey: ['workspaces'],
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/workspaces`);
      return res.json();
    }
  });

  const { data: workflowsResponse, isLoading } = useQuery<{ data: Workflow[], total: number }>({
    queryKey: ['workflows', activePage, debouncedSearch, selectedVHost, selectedWorkspace],
    // Hold the previous page/search on screen while the next one loads,
    // instead of dropping to undefined and blanking the list.
    placeholderData: keepPreviousData,
    queryFn: async () => {
      let url = `${API_BASE}/workflows?page=${activePage}&limit=${itemsPerPage}&search=${encodeURIComponent(debouncedSearch)}&vhost=${selectedVHost}`;
      if (selectedWorkspace !== 'all') {
        url += `&workspace_id=${selectedWorkspace}`;
      }
      const res = await apiFetch(url);
      return res.json();
    }
  });

  const { data: workersResponse } = useQuery<{ data: Worker[], total: number }>({
    queryKey: ['workers'],
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/workers`);
      return res.json();
    }
  });

  const allWorkflows = workflowsResponse?.data || [];
  // The server knows search, vhost and workspace; running / problems / off is
  // read off the rows it returned, so it narrows the page rather than the total.
  const workflows = allWorkflows.filter((wf) => matchesFilter(wf, filter));
  const totalItems = workflowsResponse?.total || 0;
  const workers = workersResponse?.data || [];
  // /api/workspaces answers with a bare array, but an error envelope or a future
  // {data,total} shape would land here too and take the page down with an
  // uncaught "workspaces.map is not a function". A workspace filter is not worth
  // a blank screen.
  const workspaces = Array.isArray(workspacesResponse) ? workspacesResponse : [];

  const workspaceOptions = [
    { value: 'all', label: 'All workspaces' },
    // The view you need to find what is still unorganised; the API reads
    // "none" as unassigned rather than as a workspace with that id.
    { value: 'none', label: 'No workspace' },
    ...workspaces.map((ws: Workspace) => ({ value: ws.id, label: ws.name }))
  ];

  const getWorkspaceName = (id: string) => workspaces.find((w: Workspace) => w.id === id)?.name ?? null;
  const getWorkerName = (id: string) => workers.find((w: Worker) => w.id === id)?.name ?? id;

  // 'all' is a view, not a place to put something. When it is selected the
  // first real vhost stands in for it, which is what the import path has
  // always done.
  const importTargetVHost =
    selectedVHost === 'all' ? (availableVHosts[0] || 'default') : (selectedVHost || 'default');

  const deleteMutation = useMutation({
    mutationFn: async (id: string) => {
      await apiFetch(`${API_BASE}/workflows/${id}`, { method: 'DELETE' });
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['workflows'] });
    }
  });

  const cloneMutation = useMutation({
    mutationFn: async (wf: Workflow) => {
      // Strip the identity and runtime fields; the three names exist only to be
      // omitted from `clone`.
      // eslint-disable-next-line no-unused-vars
      const { id, status, active, ...clone } = wf;
      clone.name = `${clone.name} (Copy)`;
      await apiFetch(`${API_BASE}/workflows`, {
        method: 'POST',
        body: JSON.stringify(clone)
      });
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['workflows'] });
    }
  });

  const handleExport = async (wf: Workflow) => {
    try {
      const res = await apiFetch(`${API_BASE}/workflows/${wf.id}/export`);
      const { warning } = await downloadWorkflowExport(res, wf.name);
      if (warning) {
        notifications.show({ title: 'Exported with missing dependencies', message: warning, color: 'yellow', autoClose: false });
      } else {
        notifications.show({ title: 'Exported', message: `${wf.name} was exported`, color: 'green' });
      }
    } catch (err: any) {
      notifications.show({ title: 'Export failed', message: err.message, color: 'red' });
    }
  };

  const templateImportMutation = useMutation({
    mutationFn: async (json: string) => {
      let data;
      try {
        data = JSON.parse(json);
      } catch {
        throw new Error("Invalid JSON");
      }

      // Support patching vhost if missing from the workflow in the bundle or the single workflow
      const workflow = data.workflow || data;
      const targetVHost = importTargetVHost;
      if (!workflow.vhost && targetVHost) {
        workflow.vhost = targetVHost;
      }
      // The bundle's sources and sinks carry their own vhost, and the server
      // refuses any the caller cannot write to. One without a vhost belongs in
      // the same place as the workflow it came with — the server would
      // otherwise file it under "default" while the workflow went elsewhere.
      if (targetVHost) {
        for (const resource of [...(data.sources ?? []), ...(data.sinks ?? [])]) {
          if (!resource.vhost) resource.vhost = targetVHost;
        }
      }

      const res = await apiFetch(`${API_BASE}/workflows/import`, {
        method: 'POST',
        body: JSON.stringify(data),
        silent: true,
      });
      return res.json();
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['workflows'] });
      notifications.show({ title: 'Success', message: 'Workflow imported successfully', color: 'green' });
    },
    onError: (err: any) => {
      notifications.show({
        id: 'workflow-template-import-error',
        title: 'Import Failed',
        message: err.message,
        color: 'red'
      });
    }
  });

  const batchToggleMutation = useMutation({
    mutationFn: async ({ ids, active }: { ids: string[], active: boolean }) => {
      await apiFetch(`${API_BASE}/workflows/batch/toggle`, {
        method: 'POST',
        body: JSON.stringify({ ids, active })
      });
    },
    onSuccess: (_d, { ids, active }) => {
      queryClient.invalidateQueries({ queryKey: ['workflows'] });
      setSelectedIDs([]);
      notifications.show({ message: `${ids.length} ${ids.length === 1 ? 'flow' : 'flows'} turned ${active ? 'on' : 'off'}`, color: 'green' });
    }
  });

  const batchDeleteMutation = useMutation({
    mutationFn: async (ids: string[]) => {
      await apiFetch(`${API_BASE}/workflows/batch/delete`, {
        method: 'POST',
        body: JSON.stringify({ ids })
      });
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['workflows'] });
      setSelectedIDs([]);
      notifications.show({ title: 'Success', message: 'Workflows deleted', color: 'green' });
    }
  });

  // The endpoint reports per-id results rather than one status, because a batch
  // can be partly admitted — a workspace with two free slots takes two of three.
  const batchWorkspaceMutation = useMutation({
    mutationFn: async ({ ids, workspaceID }: { ids: string[]; workspaceID: string }) => {
      const res = await apiFetch(`${API_BASE}/workflows/batch/workspace`, {
        method: 'POST',
        body: JSON.stringify({ ids, workspace_id: workspaceID }),
        silent: true,
      });
      if (!res.ok) {
        const body = await res.json().catch(() => null);
        throw new Error(body?.error || 'Failed to move workflows');
      }
      return res.json() as Promise<Record<string, string>>;
    },
    onSuccess: (results) => {
      queryClient.invalidateQueries({ queryKey: ['workflows'] });
      setSelectedIDs([]);

      const failures = Object.entries(results || {}).filter(([, r]) => r.startsWith('Error:'));
      if (failures.length === 0) {
        notifications.show({ title: 'Moved', message: 'Workflows moved', color: 'green' });
        return;
      }
      // A quota rejection is the expected failure here, and swallowing it would
      // leave the user looking at a "success" toast beside unmoved rows.
      notifications.show({
        title: `${failures.length} of ${Object.keys(results).length} could not be moved`,
        message: failures[0][1].replace(/^Error:\s*/, ''),
        color: 'orange',
        autoClose: 8000,
      });
    },
    onError: (err: any) => {
      notifications.show({ title: 'Move failed', message: err.message, color: 'red' });
    }
  });

  const toggleMutation = useMutation({
    mutationFn: async ({ id }: { id: string; active: boolean }) => {
      await apiFetch(`${API_BASE}/workflows/${id}/toggle`, { method: 'POST' });
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['workflows'] });
    }
  });

  const totalPages = Math.ceil(totalItems / itemsPerPage);
  const selected = selectedIDs.map((id) => allWorkflows.find((w) => w.id === id)).filter((w): w is Workflow => Boolean(w));

  const detailOf = (wf: Workflow) => {
    const workspace = wf.workspace_id ? (getWorkspaceName(wf.workspace_id) || wf.workspace_id) : null;
    const parts = [fromTo(wf), wf.vhost || 'default'];
    if (wf.worker_id) parts.push(getWorkerName(wf.worker_id));
    return (
      <>
        {parts.join(' · ')}
        {workspace && (
          <>
            {' · '}
            <Badge size="xs" variant="light" component="span" style={{ verticalAlign: 'middle' }}>{workspace}</Badge>
          </>
        )}
      </>
    );
  };

  return (
    <Box maw={1100} mx="auto">
      <PageHeader
        title="Flows"
        actions={canEdit && (
          <Group gap={0} wrap="nowrap">
            <Button component={Link} to="/workflows/new" leftSection={<IconPlus size="1rem" />} style={{ borderTopRightRadius: 0, borderBottomRightRadius: 0 }}>
              New flow
            </Button>
            <Menu shadow="md" width={240} position="bottom-end">
              <Menu.Target>
                <ActionIcon size={36} variant="filled" aria-label="More ways to make a flow" style={{ borderTopLeftRadius: 0, borderBottomLeftRadius: 0, borderLeft: '1px solid rgba(255,255,255,0.3)' }}>
                  <IconChevronDown size="1rem" />
                </ActionIcon>
              </Menu.Target>
              <Menu.Dropdown>
                <Menu.Item leftSection={<IconSparkles size="1rem" />} onClick={openDescribe}>Describe it</Menu.Item>
                <Menu.Item leftSection={<IconHierarchy size="1rem" />} onClick={openTemplates}>Templates</Menu.Item>
                <Menu.Item leftSection={<IconDownload size="1rem" />} onClick={openImport}>Import JSON</Menu.Item>
              </Menu.Dropdown>
            </Menu>
          </Group>
        )}
      />

      <FilterBar
        search={{ value: search, onChange: (v) => { setSearch(v); setPage(1); }, placeholder: 'Search flows' }}
        filter={{ value: filter, onChange: (v) => setFilter(v as FlowFilter), options: FLOW_FILTERS }}
      >
        <Select
          aria-label="Workspace"
          data={workspaceOptions}
          value={selectedWorkspace}
          onChange={(val) => { setSelectedWorkspace(val || 'all'); setPage(1); }}
          leftSection={<IconFolder size="1rem" />}
          allowDeselect={false}
          w={200}
        />
      </FilterBar>

      {selected.length > 0 && (
        <Group
          component="section"
          aria-label={`${selected.length} selected`}
          gap="xs"
          mb="md"
          p="sm"
          style={{ border: '1px solid var(--mantine-color-blue-4)', background: 'var(--mantine-color-blue-light)', borderRadius: 'var(--mantine-radius-md)' }}
        >
          <Text size="sm" fw={600} mr="xs">{selected.length} selected</Text>
          <Menu shadow="md" width={240}>
            <Menu.Target>
              <Button size="xs" variant="default" rightSection={<IconChevronDown size="0.9rem" />}>Move to workspace</Button>
            </Menu.Target>
            <Menu.Dropdown>
              {/* Taking flows out of every workspace is a real choice, so it is a row of its own. */}
              <Menu.Item onClick={() => batchWorkspaceMutation.mutate({ ids: selectedIDs, workspaceID: '' })}>No workspace</Menu.Item>
              {workspaces.map((ws) => (
                <Menu.Item key={ws.id} onClick={() => batchWorkspaceMutation.mutate({ ids: selectedIDs, workspaceID: ws.id })}>{ws.name}</Menu.Item>
              ))}
              {workspaces.length === 0 && <Menu.Label>No workspaces yet. Make one in Settings › Workspace.</Menu.Label>}
            </Menu.Dropdown>
          </Menu>
          <Button size="xs" variant="default" onClick={() => batchToggleMutation.mutate({ ids: selectedIDs, active: true })}>Turn on</Button>
          <Button size="xs" variant="default" onClick={() => batchToggleMutation.mutate({ ids: selectedIDs, active: false })}>Turn off</Button>
          <Button size="xs" variant="default" onClick={() => selected.forEach((wf) => handleExport(wf))}>Export JSON</Button>
          <Button
            size="xs"
            variant="default"
            c="red"
            onClick={async () => {
              if (await confirm({ title: `Delete ${selected.length} flows`, message: `Permanently delete ${selected.length} selected ${selected.length === 1 ? 'flow' : 'flows'}?`, consequence: 'Running flows are stopped. This cannot be undone.', confirmLabel: 'Delete all', danger: true })) {
                batchDeleteMutation.mutate(selectedIDs);
              }
            }}
          >
            Delete
          </Button>
          <Text size="xs" c="dimmed" visibleFrom="md">Moving checks the workspace's quota first and admits what fits.</Text>
        </Group>
      )}

      {describeOpened && (
        <Suspense fallback={null}>
          <DescribeAutomationModal
            opened
            onClose={closeDescribe}
            defaultVHost={importTargetVHost}
            availableVHosts={availableVHosts.length > 0 ? availableVHosts : ['default']}
            onOpenInEditor={(workflow) => {
              // Opened, not saved: the editor takes the draft as a new,
              // stopped workflow and saving it is the person's call.
              stashDraftWorkflow(workflow);
              closeDescribe();
              navigate({ to: '/workflows/new' });
            }}
          />
        </Suspense>
      )}

      <Modal opened={templatesOpened} onClose={closeTemplates} title="Templates" size="xl">
        <Suspense fallback={<Text size="sm">Loading templates…</Text>}>
          <TemplatesModal
            onUseTemplate={(data) => {
              templateImportMutation.mutate(JSON.stringify(data))
              closeTemplates()
            }}
          />
        </Suspense>
      </Modal>

      <Modal
        opened={importOpened}
        onClose={closeImport}
        title="Import a flow"
        size="xl"
        closeOnClickOutside={false}
      >
        <Suspense fallback={<Text size="sm">Loading the import wizard…</Text>}>
          <ImportWizard
            defaultVHost={importTargetVHost}
            availableVHosts={availableVHosts.length > 0 ? availableVHosts : ['default']}
            onCancel={closeImport}
            onImported={(name) => {
              queryClient.invalidateQueries({ queryKey: ['workflows'] });
              queryClient.invalidateQueries({ queryKey: ['import-existing'] });
              notifications.show({ title: 'Imported', message: `${name} is ready. It arrives stopped.`, color: 'green' });
              closeImport();
            }}
          />
        </Suspense>
      </Modal>

      {isLoading ? (
        <Text ta="center" py="xl" c="dimmed">Loading flows…</Text>
      ) : workflows.length === 0 ? (
        search || filter !== 'all' || selectedWorkspace !== 'all' ? (
          <EmptyState title="No flows match" detail="Try another word, or set the filter back to All." action={{ label: 'Show all', onClick: () => { setSearch(''); setFilter('all'); setSelectedWorkspace('all'); } }} />
        ) : (
          <EmptyState
            title="No flows yet"
            detail="A flow takes messages from a source, through the steps you choose, to a sink."
            action={canEdit ? { label: 'New flow', to: '/workflows/new' } : undefined}
          />
        )
      ) : (
        <RowList aria-label="Flows">
          {workflows.map((wf) => (
            <FlowRow
              key={wf.id}
              flow={wf}
              detail={detailOf(wf)}
              lead={canEdit && (
                <Checkbox
                  aria-label={`Select ${wf.name}`}
                  checked={selectedIDs.includes(wf.id)}
                  onChange={(e) => setSelectedIDs(e.currentTarget.checked ? [...selectedIDs, wf.id] : selectedIDs.filter((id) => id !== wf.id))}
                />
              )}
              onToggle={canEdit ? (f) => toggleMutation.mutate({ id: f.id, active: f.active }) : undefined}
              toggling={toggleMutation.isPending && toggleMutation.variables?.id === wf.id}
              menu={canEdit && (
                <Menu shadow="md" width={200} position="bottom-end">
                  <Menu.Target>
                    <ActionIcon variant="subtle" color="gray" aria-label={`More for ${wf.name}`}>
                      <IconDotsVertical size="1rem" />
                    </ActionIcon>
                  </Menu.Target>
                  <Menu.Dropdown>
                    <Menu.Item component={Link} to="/workflows/$id/edit" params={{ id: wf.id } as any} leftSection={<IconEdit size="1rem" />}>Edit flow</Menu.Item>
                    <Menu.Item leftSection={<IconCopy size="1rem" />} onClick={() => cloneMutation.mutate(wf)}>Clone</Menu.Item>
                    <Menu.Item leftSection={<IconDownload size="1rem" />} onClick={() => handleExport(wf)}>Export JSON</Menu.Item>
                    <Menu.Divider />
                    <Menu.Item
                      color="red"
                      leftSection={<IconTrash size="1rem" />}
                      onClick={async () => {
                        if (await confirm({ title: 'Delete flow', message: `Permanently delete ${wf.name}?`, consequence: 'If it is running it will be stopped. This cannot be undone.', confirmLabel: 'Delete', danger: true })) {
                          deleteMutation.mutate(wf.id);
                        }
                      }}
                    >
                      Delete
                    </Menu.Item>
                  </Menu.Dropdown>
                </Menu>
              )}
            />
          ))}
        </RowList>
      )}
      {totalPages > 1 && (
        <Group justify="center" mt="md">
          <Pagination total={totalPages} value={activePage} onChange={setPage} radius="md" />
        </Group>
      )}
    </Box>
  );
}
