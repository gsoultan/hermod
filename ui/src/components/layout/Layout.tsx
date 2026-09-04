import { AppShell, Burger, Group, NavLink, Text, LoadingOverlay, Box, Button, Select, Tooltip, Stack, ScrollArea, Badge, ActionIcon, useMantineColorScheme, Kbd, Menu, Avatar, rem } from '@mantine/core';
import { useDisclosure } from '@mantine/hooks';
import React, { useEffect, useRef, type ReactNode } from 'react';
import { Link, useRouterState, useNavigate } from '@tanstack/react-router';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useVHost } from '@/context/VHostContext';
import { apiFetch } from '@/api';
import { getSessionRole, getSessionUser, clearSession } from '@/auth/session';
import { Spotlight, spotlight } from '@mantine/spotlight';
import '@mantine/spotlight/styles.css';
import { notifications } from '@mantine/notifications';
import { IconActivity, IconBraces, IconChevronLeft, IconChevronRight, IconCloudUpload, IconDashboard, IconDatabase, IconGitBranch, IconGitMerge, IconHierarchy, IconHistory, IconList, IconLogout, IconMoon, IconPlus, IconPuzzle, IconRocket, IconSearch, IconServer, IconSettings, IconShieldLock, IconSun, IconUser, IconUsers, IconWorld, IconChecklist } from '@tabler/icons-react';
import type { Workflow } from '@/types';
import { ErrorBoundary } from './ErrorBoundary';
import { useConfirm } from '@/components/common/ConfirmProvider';

interface LayoutProps {
  children: ReactNode;
}

interface SideLinkProps {
  to: string;
  label: string;
  icon: React.FC<any>;
  badge?: React.ReactNode;
  children?: React.ReactNode;
  desktopOpened: boolean;
}

const SideLink = ({ to, label, icon: Icon, badge, children, desktopOpened }: SideLinkProps) => {
  const routerState = useRouterState();
  const activePage = routerState.location.pathname;
  
  const link = (
    <NavLink
      component={Link}
      to={to}
      label={desktopOpened ? label : null}
      leftSection={<Icon size="1.1rem" stroke={1.5} />}
      rightSection={desktopOpened ? badge : null}
      active={activePage === to || (to !== '/' && activePage.startsWith(to))}
      variant="light"
      styles={{
        root: {
          justifyContent: desktopOpened ? 'flex-start' : 'center',
          paddingLeft: desktopOpened ? 'var(--mantine-spacing-sm)' : 0,
          paddingRight: desktopOpened ? 'var(--mantine-spacing-sm)' : 0,
          borderRadius: 'var(--mantine-radius-md)',
          marginBottom: '4px',
          transition: 'all 0.2s ease',
          // Collapsed, the row fills its (narrowed) container and centres the
          // icon, so the active highlight reads as a symmetric tile. Fixing a
          // width here instead would overflow the container and push the tile
          // off-centre the other way.
          // gap is still applied between the icon and the two empty siblings
          // (hidden body, absent right section), adding phantom space that
          // shifts the icon ~6px left of the tile it is meant to sit in.
          ...(desktopOpened ? null : { width: '100%', height: rem(44), gap: 0 }),
        },
        // With no label to hold, the body still claims the row's free space and
        // pushes the icon left of centre — 9px off, and visible as a lopsided
        // highlight. Remove it from the layout entirely when collapsed.
        body: desktopOpened ? undefined : { display: 'none' },
        section: {
          marginRight: desktopOpened ? 'var(--mantine-spacing-xs)' : 0,
          // The trailing (empty) section keeps its own inline margin, which is
          // the last 11px of phantom space holding the icon off-centre.
          ...(desktopOpened ? null : { marginInline: 0 }),
        },
        label: {
          fontWeight: 500,
          fontSize: 'var(--mantine-font-size-sm)',
        }
      }}
    >
      {desktopOpened && children}
    </NavLink>
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
  const [mobileOpened, { toggle: toggleMobile }] = useDisclosure();
  const [desktopOpened, { toggle: toggleDesktop, open: openDesktop, close: closeDesktop }] =
    useDisclosure(true);
  const routerState = useRouterState();
  const navigate = useNavigate();
  const activePage = routerState.location.pathname;
  // Routes whose main content is an infinite canvas rather than a document.
  // These get a full-bleed shell: the surrounding card, padding and border are
  // dropped so the canvas gets the viewport instead of ~34px of chrome per axis.
  const isCanvasRoute = /^\/workflows\/(new|[^/]+\/edit)$/.test(activePage);

  // Collapsing the navbar on the editor hands another 180px to the canvas.
  // The pre-editor state is remembered and restored on the way out, and the
  // toggle still works while inside, so this is a smarter default rather than
  // something the user has to fight.
  const navbarStateBeforeCanvas = useRef<boolean | null>(null);
  useEffect(() => {
    if (isCanvasRoute) {
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
  }, [isCanvasRoute, closeDesktop, openDesktop]);
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
    const ws = new WebSocket(wsUrl);
    
    ws.onmessage = (event) => {
      try {
        const data = JSON.parse(event.data);
        queryClient.setQueryData(['dashboard-stats'], data);
      } catch (err) {
        console.error('Failed to parse dashboard stats in layout', err);
      }
    };

    return () => ws.close();
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
      const res = await apiFetch('/api/vhosts', { signal });
      if (res.ok) {
        const body = await res.json();
        const list = body && Array.isArray(body.data) ? body.data : [];
        return list.map((v: any) => String(v.name)).sort();
      }
      // Not permitted to list vhosts: derive them from what this user can see.
      const [sourcesRes, sinksRes] = await Promise.all([
        apiFetch('/api/sources', { signal }),
        apiFetch('/api/sinks', { signal }),
      ]);
      const sources = (await sourcesRes.json())?.data || [];
      const sinks = (await sinksRes.json())?.data || [];
      const names = new Set<string>();
      for (const s of sources) if (s.vhost) names.add(s.vhost);
      for (const s of sinks) if (s.vhost) names.add(s.vhost);
      return Array.from(names).sort();
    },
  });

  useEffect(() => {
    if (vhostNames) setAvailableVHosts(vhostNames);
  }, [vhostNames, setAvailableVHosts]);

  if (activePage === '/setup' || activePage === '/login' || activePage === '/forgot-password') {
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
    { value: 'all', label: 'All VHosts' },
    ...safeVhosts.map((v: string) => ({ value: v, label: v }))
  ];

  const spotlightActions = [
    {
      id: 'dashboard',
      label: 'Dashboard',
      description: 'Go to overview dashboard',
      onClick: () => navigate({ to: '/' }),
      leftSection: <IconDashboard size="1.2rem" />,
    },
    {
      id: 'workflows',
      label: 'Workflows',
      description: 'Manage data pipelines',
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
        label: `Drain DLQ: ${wf.name}`,
        description: `Prioritize dead letters before live`,
        onClick: async () => {
          if (!await confirm({ title: 'Drain dead-letter queue', message: `Drain the DLQ for "${wf.name}"?`, consequence: 'Failed messages are re-processed through the pipeline.', confirmLabel: 'Drain DLQ' })) return;
          try {
            await apiFetch(`/api/workflows/${wf.id}/drain`, { method: 'POST', silent: true });
            notifications.show({ id: `wf-drain-${wf.id}`, title: 'DLQ', message: `Requested DLQ drain for "${wf.name}"`, color: 'blue' });
          } catch (e: any) {
            notifications.show({ id: `wf-drain-error-${wf.id}`, title: 'Error', message: e.message || 'Failed to drain DLQ', color: 'red' });
          }
        },
        leftSection: <IconHistory size="1.2rem" />,
      }
    ])) : []),
    {
      id: 'sources',
      label: 'Sources',
      description: 'Manage data sources',
      onClick: () => navigate({ to: '/sources' }),
      leftSection: <IconDatabase size="1.2rem" />,
    },
    {
      id: 'sinks',
      label: 'Sinks',
      description: 'Manage data destinations',
      onClick: () => navigate({ to: '/sinks' }),
      leftSection: <IconCloudUpload size="1.2rem" />,
    },
    {
      id: 'logs',
      label: 'Logs',
      description: 'View system logs',
      onClick: () => navigate({ to: '/logs' }),
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
      description: 'Global data flow visualization',
      onClick: () => navigate({ to: '/lineage' }),
      leftSection: <IconGitMerge size="1.2rem" />,
    },
    {
      id: 'compliance',
      label: 'Compliance',
      description: 'Security and governance reports',
      onClick: () => navigate({ to: '/compliance' }),
      leftSection: <IconShieldLock size="1.2rem" />,
    },
    {
      id: 'marketplace',
      label: 'Marketplace',
      description: 'Install community plugins',
      onClick: () => navigate({ to: '/marketplace' }),
      leftSection: <IconPuzzle size="1.2rem" />,
    },
    {
      id: 'health',
      label: 'Mesh Health',
      description: 'Global cluster status',
      onClick: () => navigate({ to: '/health' }),
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
      label: 'Toggle Theme',
      description: `Switch to ${dark ? 'light' : 'dark'} mode`,
      onClick: () => toggleColorScheme(),
      leftSection: dark ? <IconSun size="1.2rem" /> : <IconMoon size="1.2rem" />,
    }
  ];

  return (
    <>
    <Spotlight
      actions={spotlightActions}
      shortcut={['mod + K', '/']}
      nothingFound="Nothing found..."
      highlightQuery
      searchProps={{
        leftSection: <IconSearch size="1.2rem" stroke={1.5} />,
        placeholder: 'Search for pages, workflows, sources...',
      }}
      limit={7}
    />
    <AppShell
      header={{ height: 60 }}
      navbar={{
        width: desktopOpened ? 260 : 80,
        breakpoint: 'sm',
        collapsed: { mobile: !mobileOpened },
      }}
      padding={isCanvasRoute ? 0 : 'md'}
    >
      <AppShell.Header withBorder bg={dark ? 'var(--mantine-color-dark-7)' : 'white'}>
        <Group h="100%" px="lg" justify="space-between">
          <Group gap="xs">
            <Burger opened={mobileOpened} onClick={toggleMobile} hiddenFrom="sm" size="sm" />
            <IconRocket size="1.8rem" color="var(--mantine-color-indigo-6)" />
            <Text fw={800} size="xl" variant="gradient" gradient={{ from: 'indigo', to: 'cyan', deg: 45 }} style={{ letterSpacing: '-0.5px' }}>
              Hermod
            </Text>
          </Group>

          <Group gap="sm">
            <Button
              variant="default"
              size="xs"
              leftSection={<IconSearch size="1rem" stroke={1.5} />}
              rightSection={<Kbd size="sm" ml={10}>Ctrl+K</Kbd>}
              onClick={spotlight.open}
              color="gray"
              visibleFrom="md"
              style={{ fontWeight: 400, color: 'var(--mantine-color-dimmed)' }}
            >
              Search...
            </Button>
            <Select
              placeholder="Select VHost"
              data={vhostOptions}
              value={selectedVHost}
              onChange={(value) => setSelectedVHost(value || 'all')}
              leftSection={<IconWorld size="1rem" stroke={1.5} />}
              size="xs"
              variant="filled"
              style={{ width: 180 }}
            />
            <ActionIcon
              aria-label="Toggle color scheme"
              variant="subtle"
              color={dark ? 'yellow' : 'gray'}
              onClick={() => toggleColorScheme()}
              title="Toggle color scheme"
              size="lg"
            >
              {dark ? <IconSun size="1.2rem" /> : <IconMoon size="1.2rem" />}
            </ActionIcon>
            <Menu shadow="md" width={200} position="bottom-end" withArrow transitionProps={{ transition: 'pop-top-right' }}>
              <Menu.Target>
                <ActionIcon aria-label="Account menu" variant="subtle" color="gray" size="lg" radius="xl" title="Account">
                  <Avatar size="sm" radius="xl" color="blue">
                    {getSessionUser()?.username?.charAt(0).toUpperCase()}
                  </Avatar>
                </ActionIcon>
              </Menu.Target>

              <Menu.Dropdown>
                <Menu.Label>Account ({getSessionUser()?.username})</Menu.Label>
                <Menu.Item 
                  leftSection={<IconUser size="1rem" stroke={1.5} />}
                  onClick={() => navigate({ to: '/profile' })}
                >
                  My Profile
                </Menu.Item>
                {isAdmin && (
                  <Menu.Item 
                    leftSection={<IconSettings size="1rem" stroke={1.5} />}
                    onClick={() => navigate({ to: '/settings' })}
                  >
                    System Settings
                  </Menu.Item>
                )}

                <Menu.Divider />

                <Menu.Label>Danger zone</Menu.Label>
                <Menu.Item 
                  color="red" 
                  leftSection={<IconLogout size="1rem" stroke={1.5} />}
                  onClick={handleLogout}
                >
                  Sign Out
                </Menu.Item>
              </Menu.Dropdown>
            </Menu>
          </Group>
        </Group>
      </AppShell.Header>

      <AppShell.Navbar p="xs" withBorder bg={dark ? 'var(--mantine-color-dark-7)' : 'white'}>
        <AppShell.Section grow component={ScrollArea} mx="-xs" px="xs">
          {/* Collapsed, sm padding either side leaves only 36px of an 80px rail
              for the highlight tile — a thin sliver with wide dead gutters. */}
          <Stack gap={4} px={desktopOpened ? 'sm' : 4} pt="md">
            {desktopOpened && (
              <Box mb={4} px="xs">
                <Text size="xs" fw={700} c="dimmed" style={{ textTransform: 'uppercase', letterSpacing: '1px' }}>
                  Main Menu
                </Text>
              </Box>
            )}
            
            <SideLink to="/" label="Dashboard" icon={IconDashboard} desktopOpened={desktopOpened}>
              <SideLink to="/" label="Overview" icon={IconActivity} desktopOpened={desktopOpened} />
              <SideLink to="/compliance" label="Compliance" icon={IconShieldLock} desktopOpened={desktopOpened} />
            </SideLink>
            <SideLink to="/sources" label="Sources" icon={IconList} desktopOpened={desktopOpened} 
              badge={dashboardStats?.active_sources > 0 && <Badge size="xs" variant="filled" color="indigo">{dashboardStats.active_sources}</Badge>} 
            />
            <SideLink to="/sinks" label="Sinks" icon={IconActivity} desktopOpened={desktopOpened} 
              badge={dashboardStats?.active_sinks > 0 && <Badge size="xs" variant="filled" color="orange">{dashboardStats.active_sinks}</Badge>}
            />
            <SideLink to="/workflows" label="Workflows" icon={IconGitBranch} desktopOpened={desktopOpened} />
            <SideLink to="/approvals" label="Approvals" icon={IconChecklist} desktopOpened={desktopOpened} />
            <SideLink to="/logs" label="Logs" icon={IconHistory} desktopOpened={desktopOpened} />
            <SideLink to="/schemas" label="Schema Registry" icon={IconBraces} desktopOpened={desktopOpened} />
            <SideLink to="/lineage" label="Data Lineage" icon={IconGitMerge} desktopOpened={desktopOpened} />
            <SideLink to="/marketplace" label="Marketplace" icon={IconPuzzle} desktopOpened={desktopOpened} />
            <SideLink to="/health" label="Mesh Health" icon={IconActivity} desktopOpened={desktopOpened} />
            
            {isAdmin && (
              <>
                {desktopOpened && (
                  <Box mt="lg" mb={4} px="xs">
                    <Text size="xs" fw={700} c="dimmed" style={{ textTransform: 'uppercase', letterSpacing: '1px' }}>
                      Administration
                    </Text>
                  </Box>
                )}
                <SideLink to="/vhosts" label="Virtual Hosts" icon={IconHierarchy} desktopOpened={desktopOpened} />
                <SideLink to="/workers" label="Workers" icon={IconServer} desktopOpened={desktopOpened} />
                <SideLink to="/users" label="Users" icon={IconUsers} desktopOpened={desktopOpened} />
                
                {desktopOpened && (
                  <Box mt="lg" mb={4} px="xs">
                    <Text size="xs" fw={700} c="dimmed" style={{ textTransform: 'uppercase', letterSpacing: '1px' }}>
                      System
                    </Text>
                  </Box>
                )}
                
                <SideLink to="/settings" label="Settings" icon={IconSettings} desktopOpened={desktopOpened} />
                <SideLink to="/audit-logs" label="Audit Logs" icon={IconHistory} desktopOpened={desktopOpened} />
              </>
            )}
          </Stack>
        </AppShell.Section>

        <AppShell.Section pt="md">
          <Button
            variant="subtle"
            color="gray"
            onClick={toggleDesktop}
            visibleFrom="sm"
            fullWidth
            leftSection={desktopOpened ? <IconChevronLeft size="1.2rem" /> : <IconChevronRight size="1.2rem" />}
            justify={desktopOpened ? 'flex-start' : 'center'}
            aria-label={desktopOpened ? 'Collapse sidebar' : 'Expand sidebar'}
            styles={{
              root: {
                height: '42px',
                borderRadius: '8px',
                // Matches the nav rows: centred chevron, no off-side drift.
                ...(desktopOpened ? null : { paddingInline: 0 }),
              },
              // Same empty-body problem as the nav rows.
              label: desktopOpened ? undefined : { display: 'none' },
              section: {
                marginRight: desktopOpened ? undefined : 0
              }
            }}
          >
            {desktopOpened && "Collapse Sidebar"}
          </Button>
        </AppShell.Section>

        <AppShell.Section p="xs">
          {/* A Go pseudo-version — v1.2.5-0.20260730072614-816c03f6a612+dirty —
              wrapped to three lines here and spilled out of the 80px rail onto
              the canvas. Only the release tag is worth standing space; the build
              stamp belongs in the tooltip, where it is still one hover away. */}
          <Tooltip
            label={versionData?.version ? `Hermod ${versionData.version}` : 'Hermod'}
            position="right"
            withArrow
            multiline
            w={280}
          >
            <Text size="xs" c="dimmed" ta="center" truncate="end" style={{ cursor: 'default' }}>
              {desktopOpened ? `Hermod ${releaseTag}` : releaseTag}
            </Text>
          </Tooltip>
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
      </AppShell.Main>
    </AppShell>
    </>
  );
}


