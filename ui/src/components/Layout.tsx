import { AppShell, Burger, Group, NavLink, Text, LoadingOverlay, Box, Button, Select, Tooltip, Stack, ScrollArea, Badge, ActionIcon, useMantineColorScheme } from '@mantine/core';
import { useDisclosure } from '@mantine/hooks';
import { IconDashboard, IconSettings, IconList, IconActivity, IconUsers, IconLogout, IconWorld, IconHierarchy, IconRocket, IconServer, IconChevronLeft, IconChevronRight, IconHistory, IconBell, IconSun, IconMoon, IconGitBranch } from '@tabler/icons-react';
import React, { useEffect } from 'react';
import { Link, useRouterState, useNavigate } from '@tanstack/react-router';
import { useVHost } from '../context/VHostContext';
import { apiFetch, getRoleFromToken } from '../api';

interface LayoutProps {
  children: React.ReactNode;
}

export function Layout({ children }: LayoutProps) {
  const [mobileOpened, { toggle: toggleMobile }] = useDisclosure();
  const [desktopOpened, { toggle: toggleDesktop }] = useDisclosure(true);
  const routerState = useRouterState();
  const navigate = useNavigate();
  const activePage = routerState.location.pathname;
  const role = getRoleFromToken();
  const isAdmin = role === 'Administrator';
  const { colorScheme, toggleColorScheme } = useMantineColorScheme();
  const dark = colorScheme === 'dark';

  const [dashboardStats, setDashboardStats] = React.useState<any>(null);

  useEffect(() => {
    // Initial fetch for dashboard stats
    if (activePage !== '/login' && activePage !== '/setup' && activePage !== '/forgot-password') {
      apiFetch('/api/dashboard/stats')
        .then(res => res.json())
        .then(data => setDashboardStats(data))
        .catch(err => console.error('Failed to fetch initial stats in layout', err));
    }
  }, [activePage]);

  useEffect(() => {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const wsUrl = `${protocol}//${window.location.host}/api/ws/dashboard`;
    const ws = new WebSocket(wsUrl);
    
    ws.onmessage = (event) => {
      try {
        const data = JSON.parse(event.data);
        setDashboardStats(data);
      } catch (err) {
        console.error('Failed to parse dashboard stats in layout', err);
      }
    };

    return () => ws.close();
  }, []);

  const SideLink = ({ to, label, icon: Icon, badge, children }: { to: string; label: string; icon: React.FC<any>; badge?: React.ReactNode; children?: React.ReactNode }) => {
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
          },
          section: {
            marginRight: desktopOpened ? 'var(--mantine-spacing-xs)' : 0,
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

  const isLoading = useRouterState({ select: (s) => s.status === 'pending' });
  const { selectedVHost, setSelectedVHost, availableVHosts, setAvailableVHosts } = useVHost();

  useEffect(() => {
    const fetchVHosts = async () => {
      try {
        const res = await apiFetch('/api/vhosts');
        if (res.ok) {
          const vhostsResponse = await res.json();
          const vhosts = vhostsResponse?.data || [];
          setAvailableVHosts(vhosts.map((v: any) => v.name).sort());
        } else {
            // Fallback to extraction if vhosts API fails or not allowed
            const [sourcesRes, sinksRes] = await Promise.all([
              apiFetch('/api/sources'),
              apiFetch('/api/sinks')
            ]);
            const sourcesResponse = await sourcesRes.json();
            const sinksResponse = await sinksRes.json();
            const sources = sourcesResponse?.data || [];
            const sinks = sinksResponse?.data || [];
            
            const vhosts = new Set<string>();
            sources.forEach((s: any) => { if (s.vhost) vhosts.add(s.vhost) });
            sinks.forEach((s: any) => { if (s.vhost) vhosts.add(s.vhost) });
            
            setAvailableVHosts(Array.from(vhosts).sort());
        }
      } catch (error) {
        console.error('Failed to fetch vhosts', error);
      }
    };

    if (activePage !== '/login' && activePage !== '/setup' && activePage !== '/forgot-password') {
      fetchVHosts();
    }
  }, [activePage, setAvailableVHosts]);

  if (activePage === '/setup' || activePage === '/login' || activePage === '/forgot-password') {
    return <main>{children}</main>;
  }

  const handleLogout = () => {
    localStorage.removeItem('hermod_token');
    navigate({ 
      to: '/login',
      search: {
        redirect: activePage,
      },
    });
  };

  const vhostOptions = [
    { value: 'all', label: 'All VHosts' },
    ...availableVHosts.map((v: string) => ({ value: v, label: v }))
  ];

  return (
    <AppShell
      header={{ height: 60 }}
      navbar={{
        width: desktopOpened ? 260 : 80,
        breakpoint: 'sm',
        collapsed: { mobile: !mobileOpened },
      }}
      padding="md"
    >
      <AppShell.Header withBorder>
        <Group h="100%" px="lg" justify="space-between">
          <Group gap="xs">
            <Burger opened={mobileOpened} onClick={toggleMobile} hiddenFrom="sm" size="sm" />
            <IconRocket size="1.8rem" color="var(--mantine-color-indigo-6)" />
            <Text fw={800} size="xl" variant="gradient" gradient={{ from: 'indigo', to: 'cyan', deg: 45 }} style={{ letterSpacing: '-0.5px' }}>
              Hermod
            </Text>
          </Group>

          <Group gap="sm">
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
            <Button 
              variant="subtle" 
              color="gray" 
              size="xs"
              leftSection={<IconLogout size="1rem" stroke={1.5} />}
              onClick={handleLogout}
            >
              Sign Out
            </Button>
          </Group>
        </Group>
      </AppShell.Header>

      <AppShell.Navbar p="xs" withBorder>
        <AppShell.Section grow component={ScrollArea} mx="-xs" px="xs">
          <Stack gap={4} px="sm" pt="md">
            {desktopOpened && (
              <Box mb={4} px="xs">
                <Text size="xs" fw={700} c="dimmed" style={{ textTransform: 'uppercase', letterSpacing: '1px' }}>
                  Main Menu
                </Text>
              </Box>
            )}
            
            <SideLink to="/" label="Dashboard" icon={IconDashboard} />
            <SideLink to="/sources" label="Sources" icon={IconList} 
              badge={dashboardStats?.active_sources > 0 && <Badge size="xs" variant="filled" color="indigo">{dashboardStats.active_sources}</Badge>} 
            />
            <SideLink to="/sinks" label="Sinks" icon={IconActivity} 
              badge={dashboardStats?.active_sinks > 0 && <Badge size="xs" variant="filled" color="orange">{dashboardStats.active_sinks}</Badge>}
            />
            <SideLink to="/workflows" label="Workflows" icon={IconGitBranch} />
            <SideLink to="/logs" label="Logs" icon={IconHistory} />
            
            {isAdmin && (
              <>
                {desktopOpened && (
                  <Box mt="lg" mb={4} px="xs">
                    <Text size="xs" fw={700} c="dimmed" style={{ textTransform: 'uppercase', letterSpacing: '1px' }}>
                      Administration
                    </Text>
                  </Box>
                )}
                <SideLink to="/vhosts" label="Virtual Hosts" icon={IconHierarchy} />
                <SideLink to="/workers" label="Workers" icon={IconServer} />
                <SideLink to="/users" label="Users" icon={IconUsers} />
                
                {desktopOpened && (
                  <Box mt="lg" mb={4} px="xs">
                    <Text size="xs" fw={700} c="dimmed" style={{ textTransform: 'uppercase', letterSpacing: '1px' }}>
                      System
                    </Text>
                  </Box>
                )}
                
                <SideLink to="/settings" label="Settings" icon={IconSettings}>
                  <SideLink to="/settings/notifications" label="Notifications" icon={IconBell} />
                </SideLink>
                <SideLink to="/audit-logs" label="Audit Logs" icon={IconHistory} />
                <SideLink to="/setup" label="Run Setup" icon={IconRocket} />
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
            styles={{
              root: {
                height: '42px',
                borderRadius: '8px',
              },
              section: {
                marginRight: desktopOpened ? undefined : 0
              }
            }}
          >
            {desktopOpened && "Collapse Sidebar"}
          </Button>
        </AppShell.Section>
      </AppShell.Navbar>

      <AppShell.Main bg="var(--mantine-color-gray-0)">
        <Box 
          p="md" 
          h="100%" 
          style={{ 
            backgroundColor: 'white',
            borderRadius: '16px',
            border: '1px solid var(--mantine-color-gray-2)',
            boxShadow: '0 1px 3px rgba(0,0,0,0.05)',
            minHeight: 'calc(100vh - 100px)'
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
          {children}
        </Box>
      </AppShell.Main>
    </AppShell>
  );
}
