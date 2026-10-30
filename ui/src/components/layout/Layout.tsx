import { AppShell, Group, NavLink, Text, LoadingOverlay, Box, Button, Select, Tooltip, Stack, ScrollArea, Badge, ActionIcon, useMantineColorScheme, Kbd, Menu, Avatar, UnstyledButton, rem } from '@mantine/core';
import { useDisclosure } from '@mantine/hooks';
import React, { useEffect, useRef, type ReactNode } from 'react';
import { Link, useRouterState, useNavigate } from '@tanstack/react-router';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useVHost } from '@/context/VHostContext';
import { apiFetch } from '@/api';
import { connectWithReconnect } from '@/lib/reconnectingSocket';
import { getSessionRole, getSessionUser, clearSession } from '@/auth/session';
import { Spotlight, spotlight } from '@mantine/spotlight';
import '@mantine/spotlight/styles.css';
import { notifications } from '@mantine/notifications';
import { IconActivity, IconBolt, IconBraces, IconChevronLeft, IconChevronRight, IconCloudUpload, IconDatabase, IconGitBranch, IconGitMerge, IconHistory, IconHome, IconList, IconLogout, IconMoon, IconPlug, IconPlus, IconPuzzle, IconRocket, IconSearch, IconSettings, IconShieldLock, IconSun, IconUser, IconWorld } from '@tabler/icons-react';
import type { Workflow } from '@/types';
import { ErrorBoundary } from './ErrorBoundary';
import { useConfirm } from '@/components/common/ConfirmProvider';
import { EnvironmentBadge } from './EnvironmentBadge';
import { PRIMARY_NAV, collapsesSidebar, isCanvasRoute as canvasRoute, sectionOf, type Section } from './nav';

interface LayoutProps {
  children: ReactNode;
}

const NAV_ICONS: Record<Section, React.FC<any>> = {
  home: IconHome,
  flows: IconGitBranch,
  connections: IconPlug,
  activity: IconActivity,
  settings: IconSettings,
};

interface SideLinkProps {
  to: string;
  label: string;
  icon: React.FC<any>;
  badge?: React.ReactNode;
  active: boolean;
  desktopOpened: boolean;
}

const SideLink = ({ to, label, icon: Icon, badge, active, desktopOpened }: SideLinkProps) => {
  const link = (
    <NavLink
      component={Link}
      to={to}
      label={desktopOpened ? label : null}
      leftSection={<Icon size="1.2rem" stroke={1.6} />}
      rightSection={desktopOpened ? badge : null}
      active={active}
      aria-current={active ? 'page' : undefined}
      variant="light"
      styles={{
        root: {
          justifyContent: desktopOpened ? 'flex-start' : 'center',
          paddingLeft: desktopOpened ? 'var(--mantine-spacing-sm)' : 0,
          paddingRight: desktopOpened ? 'var(--mantine-spacing-sm)' : 0,
          borderRadius: 'var(--mantine-radius-md)',
          height: rem(40),
          marginBottom: '2px',
          transition: 'all 0.2s ease',
          // Collapsed, the row fills the rail and centres the icon, so the
          // active highlight reads as a symmetric tile.
          ...(desktopOpened ? null : { width: '100%', height: rem(44), gap: 0 }),
        },
        // With no label to hold, the body still claims the row's free space and
        // pushes the icon left of centre. Remove it when collapsed.
        body: desktopOpened ? undefined : { display: 'none' },
        section: {
          marginRight: desktopOpened ? 'var(--mantine-spacing-xs)' : 0,
          ...(desktopOpened ? null : { marginInline: 0 }),
        },
        label: {
          fontWeight: 500,
          fontSize: 'var(--mantine-font-size-sm)',
        }
      }}
    />
  );

  if (desktopOpened) {
    return link;
  }

  return (
    <Tooltip label={label} position="right" withArrow transitionProps={{ duration: 0 }}>
      {link}
    </Tooltip>
  );
};

export function Layout({ children }: LayoutProps) {
  const confirm = useConfirm();
  const queryClient = useQueryClient();
  const [desktopOpened, { toggle: toggleDesktop, open: openDesktop, close: closeDesktop }] =
    useDisclosure(true);
  const routerState = useRouterState();
  const navigate = useNavigate();
  const activePage = routerState.location.pathname;
  const section = sectionOf(activePage);
  // The editor is an infinite canvas rather than a document. It gets a
  // full-bleed shell: the surrounding card, padding and border are dropped so
  // the canvas gets the viewport instead of ~34px of chrome per axis.
  const isCanvasRoute = canvasRoute(activePage);
  // Whenever a flow is open, read only or in the editor, the sidebar folds to
  // its icon rail so the diagram gets the width.
  const foldsSidebar = collapsesSidebar(activePage);

  // The pre-flow state is remembered and restored on the way out, and the
  // toggle still works while inside, so this is a smarter default rather than
  // something the user has to fight.
  const navbarStateBeforeCanvas = useRef<boolean | null>(null);
  useEffect(() => {
    if (foldsSidebar) {
      if (navbarStateBeforeCanvas.current === null) {
        navbarStateBeforeCanvas.current = desktopOpened;
        if (desktopOpened) closeDesktop();
      }
      return;
    }
    if (navbarStateBeforeCanvas.current !== null) {
      if (navbarStateBeforeCanvas.current) openDesktop();
      navbarStateBeforeCanvas.current = null;
    }
    // desktopOpened is intentionally omitted: this must react to route changes
    // only, or the user's own toggle inside the editor would be undone.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [foldsSidebar, closeDesktop, openDesktop]);
  // The first render on a flow route must already be folded: the effect above
  // runs after paint, and a frame of the wide sidebar on top of the canvas is
  // a visible jump.
  const navOpen = desktopOpened && !(foldsSidebar && navbarStateBeforeCanvas.current === null);
  const role = getSessionRole();
  const isAdmin = role === 'Administrator';
  const canEdit = role === 'Administrator' || role === 'Editor';
  const { colorScheme, toggleColorScheme } = useMantineColorScheme();
  const dark = colorScheme === 'dark';

  const { data: dashboardStats } = useQuery({
    queryKey: ['dashboard-stats'],
    queryFn: async () => {
      const res = await apiFetch('/api/dashboard/stats');
      return res.json();
    },
    enabled: activePage !== '/login' && activePage !== '/setup' && activePage !== '/forgot-password',
  });

  const { data: workflowsResponse } = useQuery({
    queryKey: ['workflows-spotlight'],
    queryFn: async () => {
      const res = await apiFetch('/api/workflows');
      return res.json();
    },
    enabled: activePage !== '/login' && activePage !== '/setup' && activePage !== '/forgot-password',
  });

  const { data: versionData } = useQuery({
    queryKey: ['version'],
    queryFn: async () => {
      const res = await apiFetch('/api/version');
      return res.json();
    },
    staleTime: Infinity,
    enabled: activePage !== '/login' && activePage !== '/setup' && activePage !== '/forgot-password',
  });

  const isLoading = routerState.isLoading;
  const workflows = workflowsResponse?.data || [];

  // "v1.2.5-0.20260730072614-816c03f6a612+dirty (Enterprise Edition)" → "v1.2.5".
  // Match the semver head rather than stripping the tail: Go's pseudo-version
  // suffix starts "-0.<timestamp>", so a "hyphen followed by digits" rule misses it.
  const releaseTag = (versionData?.version || '').match(/^v?\d+\.\d+\.\d+/)?.[0] || '';

  useEffect(() => {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const wsUrl = `${protocol}//${window.location.host}/api/ws/dashboard`;
    // Re-opened after a drop; opened once, as it was, the header counts
    // stopped moving after a server restart until the page was reloaded.
    return connectWithReconnect(() => wsUrl, (event) => {
      try {
        const data = JSON.parse(event.data);
        queryClient.setQueryData(['dashboard-stats'], data);
      } catch (err) {
        console.error('Failed to parse dashboard stats in layout', err);
      }
    });
  }, [queryClient]);

  const { selectedVHost, setSelectedVHost, availableVHosts, setAvailableVHosts } = useVHost();

  // The vhost list behind the header picker, read through React Query.
  //
  // This was a bare async function inside an effect: no cancellation, so a
  // route change mid-request threw "Failed to fetch" into the console and then
  // set context state from a page the user had already left; and no cache, so
  // every navigation refetched it. The query is cancelled with the route,
  // cached for a minute, and deduped across renders.
  const isPublicRoute = activePage === '/login' || activePage === '/setup' || activePage === '/forgot-password';
  //
  // Keyed under the ['vhosts'] family on purpose: VHostForm and VHostsPage
  // invalidate ['vhosts'] after a create or delete, and invalidation matches by
  // prefix, so this list refreshes with them. A key of its own would have left
  // the header picker a minute behind a vhost the user had just created.
  const { data: vhostNames } = useQuery({
    queryKey: ['vhosts', 'names'],
    enabled: !isPublicRoute,
    staleTime: 60_000,
    queryFn: async ({ signal }) => {
      // apiFetch throws on any non-OK response, so a refusal arrives here as
      // an exception — testing res.ok after it, as this used to, meant the
      // fallback below could never run. 401 is left to apiFetch's redirect.
      try {
        const res = await apiFetch('/api/vhosts', { signal, silent: true });
        const body = await res.json();
        const list = body && Array.isArray(body.data) ? body.data : [];
        return list.map((v: any) => String(v.name)).sort();
      } catch (err: any) {
        if (signal.aborted || err?.status === undefined || err.status === 401) throw err;
      }
      // Not permitted to list vhosts: derive them from what this user can see.
      // Either list may be refused too; one that is contributes nothing.
      const dataOf = async (url: string) => {
        try {
          const res = await apiFetch(url, { signal, silent: true });
          return (await res.json())?.data || [];
        } catch (err: any) {
          if (signal.aborted || err?.status === 401) throw err;
          return [];
        }
      };
      const [sources, sinks] = await Promise.all([dataOf('/api/sources'), dataOf('/api/sinks')]);
      const names = new Set<string>();
      for (const s of sources) if (s.vhost) names.add(s.vhost);
      for (const s of sinks) if (s.vhost) names.add(s.vhost);
      return Array.from(names).sort();
    },
  });

  useEffect(() => {
    if (vhostNames) setAvailableVHosts(vhostNames);
  }, [vhostNames, setAvailableVHosts]);

  if (isPublicRoute) {
    return <main>{children}</main>;
  }

  const handleLogout = async () => {
    // Ask the server to expire the session cookie. Clearing client state alone
    // is not a logout: the cookie is the credential, and it would otherwise stay
    // valid for its full 24 hours with the browser still holding it.
    try {
      await apiFetch('/api/logout', { method: 'POST', silent: true });
    } catch {
      // Even if the call fails, drop local state and leave — staying on an
      // authenticated-looking page after the user asked to leave is worse.
    }
    clearSession();
    navigate({
      to: '/login',
      search: {
        redirect: activePage,
      },
    });
  };

  const safeVhosts = Array.isArray(availableVHosts) ? availableVHosts : [];
  const vhostOptions = [
    { value: 'all', label: 'All virtual hosts' },
    ...safeVhosts.map((v: string) => ({ value: v, label: v }))
  ];

  const spotlightActions = [
    {
      id: 'home',
      label: 'Home',
      description: 'What needs you, and your flows',
      onClick: () => navigate({ to: '/' }),
      leftSection: <IconHome size="1.2rem" />,
    },
    {
      id: 'flows',
      label: 'Flows',
      description: 'Every flow, with its switch',
      onClick: () => navigate({ to: '/workflows' }),
      leftSection: <IconGitBranch size="1.2rem" />,
    },
    ...workflows.slice(0, 10).map((wf: Workflow) => ({
      id: `wf-${wf.id}`,
      label: `Workflow: ${wf.name}`,
      description: `VHost: ${wf.vhost} | Status: ${wf.active ? 'Active' : 'Inactive'}`,
      onClick: () => navigate({ to: `/workflows/$id`, params: { id: wf.id } as any }),
      leftSection: <IconGitBranch size="1.2rem" color={wf.active ? 'var(--mantine-color-green-6)' : 'var(--mantine-color-gray-6)'} />,
    })),
    {
      id: 'create-workflow',
      label: 'Create Workflow',
      description: 'Create a new data pipeline',
      onClick: () => navigate({ to: '/workflows/new' }),
      leftSection: <IconPlus size="1.2rem" />,
    },
    // Action-oriented commands
    ...(canEdit ? [{
      id: 'start-all-vhost',
      label: 'Start all workflows in current VHost',
      description: selectedVHost && selectedVHost !== 'all' ? `VHost: ${selectedVHost}` : 'Select a VHost first',
      onClick: async () => {
        if (!selectedVHost || selectedVHost === 'all') {
          notifications.show({ title: 'Select VHost', message: 'Please select a specific VHost to start all workflows.', color: 'orange' });
          return;
        }
        if (!await confirm({ title: 'Start all workflows', message: `Start every workflow in VHost "${selectedVHost}"?`, confirmLabel: 'Start all' })) return;
        try {
          const res = await apiFetch(`/api/workflows?vhost=${encodeURIComponent(selectedVHost)}`, { silent: true });
          const data = await res.json();
          const ids = (data.data || []).map((w: any) => w.id);
          if (ids.length === 0) {
            notifications.show({ id: 'start-all-no-workflows', title: 'No Workflows', message: `No workflows found in VHost ${selectedVHost}.`, color: 'gray' });
            return;
          }
          await apiFetch('/api/workflows/batch/toggle', { method: 'POST', body: JSON.stringify({ ids, active: true }), silent: true });
          notifications.show({ id: 'start-all-success', title: 'Started', message: `Requested start for ${ids.length} workflows in ${selectedVHost}.`, color: 'green' });
        } catch (e: any) {
          notifications.show({ id: 'start-all-error', title: 'Error', message: e.message || 'Failed to start workflows', color: 'red' });
        }
      },
      leftSection: <IconRocket size="1.2rem" />,
    }] : []),
    ...(canEdit ? [{
      id: 'stop-all-vhost',
      label: 'Stop all workflows in current VHost',
      description: selectedVHost && selectedVHost !== 'all' ? `VHost: ${selectedVHost}` : 'Select a VHost first',
      onClick: async () => {
        if (!selectedVHost || selectedVHost === 'all') {
          notifications.show({ title: 'Select VHost', message: 'Please select a specific VHost to stop all workflows.', color: 'orange' });
          return;
        }
        if (!await confirm({ title: 'Stop all workflows', message: `Stop every workflow in VHost "${selectedVHost}"?`, consequence: 'In-flight messages are drained, but ingestion stops immediately.', confirmLabel: 'Stop all', danger: true })) return;
        try {
          const res = await apiFetch(`/api/workflows?vhost=${encodeURIComponent(selectedVHost)}`, { silent: true });
          const data = await res.json();
          const ids = (data.data || []).map((w: any) => w.id);
          if (ids.length === 0) {
            notifications.show({ id: 'stop-all-no-workflows', title: 'No Workflows', message: `No workflows found in VHost ${selectedVHost}.`, color: 'gray' });
            return;
          }
          await apiFetch('/api/workflows/batch/toggle', { method: 'POST', body: JSON.stringify({ ids, active: false }), silent: true });
          notifications.show({ id: 'stop-all-success', title: 'Stopped', message: `Requested stop for ${ids.length} workflows in ${selectedVHost}.`, color: 'green' });
        } catch (e: any) {
          notifications.show({ id: 'stop-all-error', title: 'Error', message: e.message || 'Failed to stop workflows', color: 'red' });
        }
      },
      leftSection: <IconActivity size="1.2rem" />,
    }] : []),
    // Per-workflow quick actions (top 5)
    ...(canEdit ? workflows.slice(0, 5).flatMap((wf: Workflow) => ([
      {
        id: `start-${wf.id}`,
        label: `Start: ${wf.name}`,
        description: `VHost: ${wf.vhost}`,
        onClick: async () => {
          try {
            await apiFetch(`/api/workflows/${wf.id}/toggle`, { method: 'POST', silent: true });
            notifications.show({ id: `wf-start-${wf.id}`, title: 'Workflow', message: `Toggled start for "${wf.name}"`, color: 'green' });
          } catch (e: any) {
            notifications.show({ id: `wf-start-error-${wf.id}`, title: 'Error', message: e.message || 'Failed to start workflow', color: 'red' });
          }
        },
        leftSection: <IconRocket size="1.2rem" />,
      },
      {
        id: `stop-${wf.id}`,
        label: `Stop: ${wf.name}`,
        description: `VHost: ${wf.vhost}`,
        onClick: async () => {
          if (!await confirm({ title: 'Stop workflow', message: `Stop "${wf.name}"?`, confirmLabel: 'Stop', danger: true })) return;
          try {
            await apiFetch(`/api/workflows/${wf.id}/toggle`, { method: 'POST', silent: true });
            notifications.show({ id: `wf-stop-${wf.id}`, title: 'Workflow', message: `Toggled stop for "${wf.name}"`, color: 'green' });
          } catch (e: any) {
            notifications.show({ id: `wf-stop-error-${wf.id}`, title: 'Error', message: e.message || 'Failed to stop workflow', color: 'red' });
          }
        },
        leftSection: <IconActivity size="1.2rem" />,
      },
      {
        id: `drain-${wf.id}`,
        label: `Replay dead letters: ${wf.name}`,
        description: `Replay the failed messages waiting on the Dead Letters page`,
        onClick: async () => {
          if (!await confirm({ title: 'Replay dead letters', message: `Replay the dead letters waiting for "${wf.name}"?`, consequence: 'Each one is sent again from the node or sink that failed it. Those that go through leave the Dead Letters page; those that fail again stay.', confirmLabel: 'Replay' })) return;
          try {
            await apiFetch(`/api/workflows/${wf.id}/drain`, { method: 'POST', silent: true });
            notifications.show({ id: `wf-drain-${wf.id}`, title: 'Dead letters', message: `Replaying the dead letters waiting for "${wf.name}"`, color: 'blue' });
          } catch (e: any) {
            notifications.show({ id: `wf-drain-error-${wf.id}`, title: 'Error', message: e.message || 'Could not replay the dead letters', color: 'red' });
          }
        },
        leftSection: <IconHistory size="1.2rem" />,
      }
    ])) : []),
    {
      id: 'connections',
      label: 'Connections',
      description: 'Sources, sinks and community connectors',
      onClick: () => navigate({ to: '/connections' }),
      leftSection: <IconPlug size="1.2rem" />,
    },
    {
      id: 'activity',
      label: 'Activity',
      description: 'Logs, dead letters, mesh health and insights',
      onClick: () => navigate({ to: '/activity' }),
      leftSection: <IconActivity size="1.2rem" />,
    },
    {
      id: 'sources',
      label: 'Sources',
      description: 'Manage data sources',
      onClick: () => navigate({ to: '/connections', search: { tab: 'sources' } as any }),
      leftSection: <IconDatabase size="1.2rem" />,
    },
    {
      id: 'sinks',
      label: 'Sinks',
      description: 'Manage data destinations',
      onClick: () => navigate({ to: '/connections', search: { tab: 'sinks' } as any }),
      leftSection: <IconCloudUpload size="1.2rem" />,
    },
    {
      id: 'logs',
      label: 'Logs',
      description: 'View system logs',
      onClick: () => navigate({ to: '/activity', search: { tab: 'logs' } as any }),
      leftSection: <IconList size="1.2rem" />,
    },
    {
      id: 'schemas',
      label: 'Schemas',
      description: 'Manage data contracts',
      onClick: () => navigate({ to: '/schemas' }),
      leftSection: <IconBraces size="1.2rem" />,
    },
    {
      id: 'lineage',
      label: 'Data Lineage',
      description: 'Data flow per vhost, and what breaks if a source changes',
      onClick: () => navigate({ to: '/activity', search: { tab: 'lineage' } as any }),
      leftSection: <IconGitMerge size="1.2rem" />,
    },
    {
      id: 'compliance',
      label: 'Compliance',
      description: 'Security and governance reports',
      onClick: () => navigate({ to: '/activity', search: { tab: 'compliance' } as any }),
      leftSection: <IconShieldLock size="1.2rem" />,
    },
    {
      id: 'marketplace',
      label: 'Marketplace',
      description: 'Install community plugins',
      onClick: () => navigate({ to: '/connections', search: { tab: 'community' } as any }),
      leftSection: <IconPuzzle size="1.2rem" />,
    },
    {
      id: 'health',
      label: 'Mesh Health',
      description: 'Global cluster status',
      onClick: () => navigate({ to: '/activity', search: { tab: 'health' } as any }),
      leftSection: <IconActivity size="1.2rem" />,
    },
    {
      id: 'settings',
      label: 'Settings',
        description: 'System configuration',
        onClick: () => navigate({ to: '/settings' }),
        leftSection: <IconSettings size="1.2rem" />,
    },
    {
      id: 'toggle-theme',
      label: 'Toggle theme',
      description: `Switch to ${dark ? 'light' : 'dark'} mode`,
      onClick: () => toggleColorScheme(),
      leftSection: dark ? <IconSun size="1.2rem" /> : <IconMoon size="1.2rem" />,
    }
  ];

  const user = getSessionUser();
  const initial = user?.username?.charAt(0).toUpperCase() || '?';
  const vhostLabel = selectedVHost && selectedVHost !== 'all' ? selectedVHost : 'All virtual hosts';
  const problems = Number(dashboardStats?.failed_workflows) || 0;

  const navLinks = (compact: boolean) => PRIMARY_NAV.map((item) => (
    <SideLink
      key={item.key}
      to={item.to}
      label={item.label}
      icon={NAV_ICONS[item.key]}
      active={section === item.key}
      desktopOpened={compact}
      badge={item.key === 'flows' && problems > 0
        ? <Badge size="xs" variant="filled" color="red" aria-label={`${problems} flows with problems`}>{problems}</Badge>
        : null}
    />
  ));

  const accountMenu = (
    <Menu
      shadow="md"
      width={260}
      position={navOpen ? 'top-start' : 'right-end'}
      withArrow
      trapFocus={false}
      transitionProps={{ transition: 'pop' }}
    >
      <Menu.Target>
        <UnstyledButton
          aria-label="Account menu"
          title="Account"
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: rem(10),
            width: '100%',
            padding: navOpen ? `${rem(6)} ${rem(8)}` : rem(6),
            justifyContent: navOpen ? 'flex-start' : 'center',
            borderRadius: 'var(--mantine-radius-md)',
          }}
        >
          <Avatar size="sm" radius="xl" color="indigo">{initial}</Avatar>
          {navOpen && (
            <Box style={{ minWidth: 0 }}>
              <Text size="sm" fw={500} truncate="end">{user?.username}</Text>
              <Text size="xs" c="dimmed" truncate="end">{vhostLabel}</Text>
            </Box>
          )}
        </UnstyledButton>
      </Menu.Target>

      <Menu.Dropdown>
        <Menu.Label>{user?.username} · {role}</Menu.Label>
        <Box px="sm" pb="xs">
          <Select
            label="Virtual host"
            size="xs"
            data={vhostOptions}
            value={selectedVHost}
            onChange={(value) => setSelectedVHost(value || 'all')}
            leftSection={<IconWorld size="1rem" stroke={1.5} />}
            comboboxProps={{ withinPortal: false }}
            allowDeselect={false}
          />
        </Box>
        <Menu.Divider />
        <Menu.Item leftSection={<IconUser size="1rem" stroke={1.5} />} onClick={() => navigate({ to: '/profile' })}>
          My account
        </Menu.Item>
        <Menu.Item
          leftSection={dark ? <IconSun size="1rem" stroke={1.5} /> : <IconMoon size="1rem" stroke={1.5} />}
          onClick={() => toggleColorScheme()}
        >
          {dark ? 'Switch to light mode' : 'Switch to dark mode'}
        </Menu.Item>
        {isAdmin && (
          <Menu.Item leftSection={<IconSettings size="1rem" stroke={1.5} />} onClick={() => navigate({ to: '/settings' })}>
            Settings
          </Menu.Item>
        )}
        <Menu.Divider />
        <Menu.Item color="red" leftSection={<IconLogout size="1rem" stroke={1.5} />} onClick={handleLogout}>
          Sign out
        </Menu.Item>
        <Menu.Label>
          <Tooltip label={versionData?.version ? `Hermod ${versionData.version}` : 'Hermod'} multiline w={280} withArrow>
            <span>Hermod {releaseTag}</span>
          </Tooltip>
        </Menu.Label>
      </Menu.Dropdown>
    </Menu>
  );

  return (
    <>
    <Spotlight
      actions={spotlightActions}
      shortcut={['mod + K', '/']}
      nothingFound="Nothing found..."
      highlightQuery
      searchProps={{
        leftSection: <IconSearch size="1.2rem" stroke={1.5} />,
        placeholder: 'Search for pages, flows, connections...',
      }}
      limit={7}
    />
    <AppShell
      navbar={{
        width: navOpen ? 232 : 64,
        breakpoint: 'sm',
        // Phones get the bottom tabs instead of a drawer.
        collapsed: { mobile: true },
      }}
      padding={isCanvasRoute ? 0 : 'md'}
    >
      <AppShell.Navbar p={navOpen ? 'sm' : 'xs'} withBorder bg={dark ? 'var(--mantine-color-dark-7)' : 'white'}>
        <AppShell.Section>
          <Group justify={navOpen ? 'space-between' : 'center'} gap="xs" wrap="nowrap" pb="sm" pt={4} px={navOpen ? 4 : 0}>
            <Group gap={8} wrap="nowrap" style={{ minWidth: 0 }}>
              <Box
                aria-hidden
                style={{
                  width: rem(28), height: rem(28), borderRadius: rem(8), display: 'grid', placeItems: 'center',
                  background: 'var(--mantine-color-indigo-6)', color: 'white', flex: 'none',
                }}
              >
                <IconBolt size="1rem" stroke={2} />
              </Box>
              {navOpen && <Text fw={700} size="md" truncate="end">Hermod</Text>}
            </Group>
            {navOpen && (
              <ActionIcon
                variant="subtle"
                color="gray"
                onClick={toggleDesktop}
                aria-label="Collapse sidebar"
                title="Collapse sidebar"
                visibleFrom="sm"
              >
                <IconChevronLeft size="1rem" />
              </ActionIcon>
            )}
          </Group>
          {!navOpen && (
            <Tooltip label="Expand sidebar" position="right" withArrow transitionProps={{ duration: 0 }}>
              <ActionIcon
                variant="default"
                onClick={toggleDesktop}
                aria-label="Expand sidebar"
                mb="xs"
                style={{ width: '100%', height: rem(28) }}
              >
                <IconChevronRight size="0.9rem" />
              </ActionIcon>
            </Tooltip>
          )}
          {navOpen && <Box px={4} pb={6}><EnvironmentBadge /></Box>}
        </AppShell.Section>

        <AppShell.Section grow component={ScrollArea} mx={navOpen ? '-sm' : '-xs'} px={navOpen ? 'sm' : 'xs'}>
          <Stack component="nav" aria-label="Main" gap={0}>
            {navLinks(navOpen)}
          </Stack>
        </AppShell.Section>

        <AppShell.Section pt="xs">
          <Stack gap={2}>
            {navOpen ? (
              <Button
                variant="subtle"
                color="gray"
                fullWidth
                justify="flex-start"
                leftSection={<IconSearch size="1.1rem" stroke={1.6} />}
                rightSection={<Kbd size="xs">⌘K</Kbd>}
                onClick={spotlight.open}
                styles={{ root: { height: rem(40), fontWeight: 500, paddingInline: 'var(--mantine-spacing-sm)' }, inner: { justifyContent: 'space-between' } }}
              >
                Search
              </Button>
            ) : (
              <Tooltip label="Search (⌘K)" position="right" withArrow transitionProps={{ duration: 0 }}>
                <ActionIcon variant="subtle" color="gray" onClick={spotlight.open} aria-label="Search" style={{ width: '100%', height: rem(44) }}>
                  <IconSearch size="1.2rem" stroke={1.6} />
                </ActionIcon>
              </Tooltip>
            )}
            {accountMenu}
          </Stack>
        </AppShell.Section>
      </AppShell.Navbar>

      <AppShell.Main bg={dark ? 'var(--mantine-color-dark-7)' : 'var(--mantine-color-gray-0)'}>
        <Box
          p={isCanvasRoute ? 0 : 'md'}
          h="100%"
          style={{
            backgroundColor: dark ? 'var(--mantine-color-dark-6)' : 'var(--mantine-color-white)',
            // The workflow editor is a canvas, not a document. Wrapping an
            // infinite canvas in a rounded, bordered card costs ~34px of width
            // and height and frames the workspace as a content block sitting on
            // a page. Full-bleed on that route only.
            borderRadius: isCanvasRoute ? 0 : '16px',
            border: isCanvasRoute
              ? 'none'
              : `1px solid ${dark ? 'var(--mantine-color-dark-5)' : 'var(--mantine-color-gray-2)'}`,
            boxShadow: isCanvasRoute ? 'none' : '0 1px 3px rgba(0,0,0,0.05)',
            // The canvas route sizes itself from the viewport (see
            // WorkflowEditorPage), so this wrapper must not impose or clip a
            // height of its own.
            minHeight: isCanvasRoute ? 0 : 'calc(100vh - 100px)',
            overflow: isCanvasRoute ? 'hidden' : undefined,
          }}
        >
          <LoadingOverlay
            visible={isLoading}
            zIndex={1000}
            overlayProps={{
              radius: "md",
              blur: 2,
              // Do not block user interactions (typing/clicks) if overlay remains visible longer than expected
              style: { pointerEvents: 'none' }
            }}
          />
          <ErrorBoundary>
            {children}
          </ErrorBoundary>
        </Box>
        {/* Room for the bottom tabs on a phone; nothing on wider screens. */}
        <Box hiddenFrom="sm" h={64} />
      </AppShell.Main>

      <Box
        component="nav"
        aria-label="Bottom tabs"
        hiddenFrom="sm"
        style={{
          position: 'fixed', left: 0, right: 0, bottom: 0, zIndex: 200,
          display: 'grid', gridTemplateColumns: `repeat(${PRIMARY_NAV.length}, 1fr)`,
          borderTop: `1px solid ${dark ? 'var(--mantine-color-dark-5)' : 'var(--mantine-color-gray-3)'}`,
          background: dark ? 'var(--mantine-color-dark-7)' : 'white',
          paddingBottom: 'env(safe-area-inset-bottom)',
        }}
      >
        {PRIMARY_NAV.map((item) => {
          const Icon = NAV_ICONS[item.key];
          const active = section === item.key;
          return (
            <UnstyledButton
              key={item.key}
              component={Link}
              to={item.to}
              aria-current={active ? 'page' : undefined}
              style={{
                display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 2,
                minHeight: rem(56), fontSize: rem(11), fontWeight: 500,
                color: active ? 'var(--mantine-color-indigo-6)' : 'var(--mantine-color-dimmed)',
              }}
            >
              <Icon size="1.4rem" stroke={1.6} />
              {item.label}
            </UnstyledButton>
          );
        })}
      </Box>
    </AppShell>
    </>
  );
}
